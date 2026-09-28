package herdr

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/herdr/herdrtest"
)

// A pane input asks Herdr's status for the socket once, then types every
// input as one pane.send_text request on the session's socket: control keys,
// a multi-byte paste and the board's largest input alike, and no process.
func TestPaneInputTypesEachInputAsOneSocketRequest(t *testing.T) {
	inputs := []struct {
		name string
		text string
	}{
		{"a key", "x"},
		{"enter", "\r"},
		{"an arrow key", "\x1b[A"},
		{"a multi-byte paste", strings.Repeat("ab日", 3000)},
		{"the largest input", strings.Repeat("a", 64<<10)},
	}
	socket := herdrtest.NewSocket(t)
	runner := &fakeRunner{replies: []runnerReply{rawReply(socket.Status())}}
	var sleeps []time.Duration
	client := newTestClient(runner, &sleeps)

	panes, err := client.PaneInput(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range inputs {
		if err := panes.SendText(context.Background(), "w1:p2", input.text); err != nil {
			t.Fatalf("typing %s: %v", input.name, err)
		}
	}

	requests := socket.Requests()
	if len(requests) != len(inputs) {
		t.Fatalf("socket got %d requests, want one per input (%d)", len(requests), len(inputs))
	}
	for i, input := range inputs {
		got := requests[i]
		if got.Method != "pane.send_text" || got.Params["pane_id"] != "w1:p2" || got.Params["text"] != input.text {
			t.Fatalf("request for %s = %s to %v with %d bytes, want pane.send_text to w1:p2 with the input", input.name, got.Method, got.Params["pane_id"], len(fmt.Sprint(got.Params["text"])))
		}
	}
	if calls := runner.Requests(); len(calls) != 1 || strings.Join(calls[0].Args, " ") != "status --json --session fleet" {
		t.Fatalf("commands run = %v, want only the one status read", calls)
	}
}

// History reads the pane's most recent lines, history included, with their
// colors kept, as many as asked for.
func TestPaneInputReadsThePanesRecentHistory(t *testing.T) {
	cases := []struct {
		name  string
		lines int
		text  string
	}{
		{"the last lines", 2, "b\n\x1b[1mc\x1b[0m"},
		{"a shorter history whole", 100, "a\nb\n\x1b[1mc\x1b[0m"},
	}
	socket := herdrtest.NewSocket(t)
	socket.History = []string{"a", "b", "\x1b[1mc\x1b[0m"}
	panes := Socket{pipe: `\\.\pipe\` + socket.Path}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			text, err := panes.History(context.Background(), "w1:p2", c.lines)

			if err != nil || text != c.text {
				t.Fatalf("History(%d) = %q, %v; want %q", c.lines, text, err, c.text)
			}
			last := socket.Requests()[len(socket.Requests())-1]
			if last.Method != "pane.read" || last.Params["pane_id"] != "w1:p2" || last.Params["source"] != "recent" || last.Params["lines"] != float64(c.lines) || last.Params["format"] != "ansi" || last.Params["strip_ansi"] != false {
				t.Fatalf("request = %s %v, want pane.read of w1:p2's recent %d lines as ANSI", last.Method, last.Params, c.lines)
			}
		})
	}
}

// A read answer that carries no text is a failure, never an empty history.
func TestPaneInputHistoryFailsWithoutText(t *testing.T) {
	socket := herdrtest.NewSocket(t)
	socket.Answer = func(r herdrtest.Request) string {
		return `{"id":"` + r.ID + `","result":{"type":"pane_read","read":{"pane_id":"w1:p2"}}}`
	}

	_, err := Socket{pipe: `\\.\pipe\` + socket.Path}.History(context.Background(), "w1:p2", 5)

	if err == nil || !strings.Contains(err.Error(), "carries no text") {
		t.Fatalf("History = %v, want an error naming the missing text", err)
	}
}

// Anything but Herdr's ok for this request is a failed input, never a silent
// success: an error answer, no answer at all, or an answer to another request.
func TestSocketRequestFailsWithoutItsOwnOkAnswer(t *testing.T) {
	cases := []struct {
		name   string
		answer func(herdrtest.Request) string
		want   string
	}{
		{"an error answer", func(r herdrtest.Request) string {
			return `{"id":"` + r.ID + `","error":{"code":"pane_not_found","message":"no pane w1:p2"}}`
		}, "pane_not_found: no pane w1:p2"},
		{"no answer", func(herdrtest.Request) string { return "" }, "no answer"},
		{"another request's answer", func(herdrtest.Request) string { return `{"id":"other","result":{"type":"ok"}}` }, `answer is for request "other"`},
		{"an answer without a result", func(r herdrtest.Request) string { return `{"id":"` + r.ID + `"}` }, "no result"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			socket := herdrtest.NewSocket(t)
			socket.Answer = c.answer
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			err := Socket{pipe: `\\.\pipe\` + socket.Path}.SendText(ctx, "w1:p2", "x")

			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("SendText = %v, want an error naming %q", err, c.want)
			}
		})
	}
}

// A socket is only taken from a status that names a running server's socket.
func TestSocketRefusesAStatusWithoutARunningServersSocket(t *testing.T) {
	cases := []struct {
		name   string
		status string
		want   string
	}{
		{"not running", `{"server":{"running":false,"socket":"C:\\herdr.sock"}}`, "not running"},
		{"no socket", `{"server":{"running":true}}`, "names no socket"},
		{"malformed", `{`, "decode status"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			runner := &fakeRunner{replies: []runnerReply{rawReply(c.status)}}
			var sleeps []time.Duration

			_, err := newTestClient(runner, &sleeps).Socket(context.Background())

			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("Socket = %v, want %q", err, c.want)
			}
		})
	}
}

// A socket no server listens on fails at once rather than hanging.
func TestSocketWithoutAServerFails(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := Socket{pipe: `\\.\pipe\` + t.TempDir() + `\herdr.sock`}.SendText(ctx, "w1:p2", "x")

	if err == nil || !strings.Contains(err.Error(), "connect to") {
		t.Fatalf("SendText with no server = %v, want a connect error", err)
	}
}

// With a socket cache, a session's snapshot and a pane's process info are read
// on Herdr's socket, a pipe round trip of milliseconds, instead of starting a
// herdr process for each, which takes about a second on a loaded machine: the
// whole cache asks Herdr's status once and runs no other command.
func TestSocketCacheReadsSnapshotAndProcessInfoOnTheSocket(t *testing.T) {
	socket := herdrtest.NewSocket(t)
	socket.Answer = func(request herdrtest.Request) string {
		switch request.Method {
		case "session.snapshot":
			return `{"id":"session.snapshot","result":{"type":"session_snapshot","snapshot":{"version":"0.9","protocol":22,"workspaces":[],"tabs":[],"panes":[{"pane_id":"w1:p2","tab_id":"w1:t1","workspace_id":"w1","terminal_id":"term_1"}],"agents":[],"layouts":[]}}}`
		case "pane.process_info":
			return `{"id":"pane.process_info","result":{"type":"pane_process_info","process_info":{"shell_pid":10,"foreground_process_group_id":20}}}`
		}
		return ""
	}
	runner := &fakeRunner{replies: []runnerReply{rawReply(socket.Status())}}
	var sleeps []time.Duration
	client := newTestClient(runner, &sleeps)
	client.Sockets = NewSocketCache()

	for i := 0; i < 2; i++ {
		snapshot, err := client.Snapshot(context.Background())
		if err != nil || len(snapshot.Panes) != 1 || snapshot.Panes[0].TerminalID != "term_1" {
			t.Fatalf("snapshot %d = %+v, %v; want the socket's one pane", i, snapshot, err)
		}
		info, err := client.PaneProcessInfo(context.Background(), Target{Session: "fleet", Pane: "w1:p2"})
		if err != nil || info.ShellPID != 10 || info.ForegroundProcessGroupID != 20 {
			t.Fatalf("process info %d = %+v, %v; want the socket's pids", i, info, err)
		}
	}

	if calls := runner.Requests(); len(calls) != 1 || strings.Join(calls[0].Args, " ") != "status --json --session fleet" {
		t.Fatalf("commands run = %v, want only the one status read", calls)
	}
	if requests := socket.Requests(); len(requests) != 4 {
		t.Fatalf("socket got %d requests, want 2 snapshots and 2 process infos", len(requests))
	}
}

// The monitor's other two reads each minute, the agent list and every task's
// bounded pane capture, go over the socket too: with a cache the whole scan
// starts no herdr process after the one status read.
func TestSocketCacheReadsAgentsAndPaneEvidenceOnTheSocket(t *testing.T) {
	// Arrange
	socket := herdrtest.NewSocket(t)
	socket.Answer = func(request herdrtest.Request) string {
		switch request.Method {
		case "agent.list":
			return `{"id":"agent.list","result":{"type":"agent_list","agents":[{"agent":"claude","agent_status":"working","pane_id":"w1:p2","revision":7,"state_change_seq":3}]}}`
		case "pane.read":
			if request.Params["source"] != "recent_unwrapped" || request.Params["lines"] != float64(200) {
				return `{"id":"pane.read","error":{"code":"bad_params","message":"want the 200 most recent unwrapped lines"}}`
			}
			return `{"id":"pane.read","result":{"type":"pane_read","read":{"text":"$ go test\nok\n"}}}`
		}
		return ""
	}
	runner := &fakeRunner{replies: []runnerReply{rawReply(socket.Status())}}
	var sleeps []time.Duration
	client := newTestClient(runner, &sleeps)
	client.Sockets = NewSocketCache()

	for i := 0; i < 2; i++ {
		// Act
		agents, agentsErr := client.AgentList(context.Background())
		evidence, evidenceErr := client.CaptureEvidence(context.Background(), Target{Session: "fleet", Pane: "w1:p2"})

		// Assert
		if agentsErr != nil || len(agents) != 1 || agents[0].Revision != 7 || agents[0].StateChangeSeq != 3 {
			t.Fatalf("agents %d = %+v, %v; want the socket's one agent with its counters", i, agents, agentsErr)
		}
		if evidenceErr != nil || string(evidence) != "$ go test\nok\n" {
			t.Fatalf("evidence %d = %q, %v; want the socket's pane text", i, evidence, evidenceErr)
		}
	}
	if calls := runner.Requests(); len(calls) != 1 || strings.Join(calls[0].Args, " ") != "status --json --session fleet" {
		t.Fatalf("commands run = %v, want only the one status read", calls)
	}
	if requests := socket.Requests(); len(requests) != 4 {
		t.Fatalf("socket got %d requests, want 2 agent lists and 2 pane reads", len(requests))
	}
}

// A socket that cannot be reached is forgotten and the read goes through the
// herdr command, so a restarted Herdr is found again on the next read.
func TestSocketCacheFallsBackToTheCommandWhenThePipeIsGone(t *testing.T) {
	path, err := json.Marshal(filepath.Join(t.TempDir(), "herdr.sock"))
	if err != nil {
		t.Fatal(err)
	}
	gone := `{"client":{"protocol":22},"server":{"status":"running","running":true,"protocol":22,"compatible":true,"socket":` + string(path) + `}}`
	snapshot := `{"id":"cli:api:snapshot","result":{"type":"session_snapshot","snapshot":{"version":"0.9","protocol":22,"workspaces":[],"tabs":[],"panes":[],"agents":[],"layouts":[]}}}`
	runner := &fakeRunner{replies: []runnerReply{rawReply(gone), rawReply(snapshot), rawReply(gone), rawReply(snapshot)}}
	var sleeps []time.Duration
	client := newTestClient(runner, &sleeps)
	client.Sockets = NewSocketCache()

	for i := 0; i < 2; i++ {
		if _, err := client.Snapshot(context.Background()); err != nil {
			t.Fatalf("snapshot %d through the command: %v", i, err)
		}
	}

	var got []string
	for _, call := range runner.Requests() {
		got = append(got, strings.Join(call.Args, " "))
	}
	want := []string{"status --json --session fleet", "api snapshot --session fleet", "status --json --session fleet", "api snapshot --session fleet"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("commands run = %q, want %q: the unreachable pipe is not kept", got, want)
	}
}

// A live Herdr's error answer keeps the socket: the read goes through the
// herdr command, and the next read uses the same socket without asking
// Herdr's status again.
func TestSocketCacheKeepsTheSocketAfterAnErrorAnswer(t *testing.T) {
	socket := herdrtest.NewSocket(t)
	socket.Answer = func(request herdrtest.Request) string {
		return `{"id":"` + request.ID + `","error":{"code":"internal","message":"try again"}}`
	}
	snapshot := `{"id":"cli:api:snapshot","result":{"type":"session_snapshot","snapshot":{"version":"0.9","protocol":22,"workspaces":[],"tabs":[],"panes":[],"agents":[],"layouts":[]}}}`
	runner := &fakeRunner{replies: []runnerReply{rawReply(socket.Status()), rawReply(snapshot), rawReply(snapshot)}}
	var sleeps []time.Duration
	client := newTestClient(runner, &sleeps)
	client.Sockets = NewSocketCache()

	for i := 0; i < 2; i++ {
		if _, err := client.Snapshot(context.Background()); err != nil {
			t.Fatalf("snapshot %d through the command: %v", i, err)
		}
	}

	var got []string
	for _, call := range runner.Requests() {
		got = append(got, strings.Join(call.Args, " "))
	}
	want := []string{"status --json --session fleet", "api snapshot --session fleet", "api snapshot --session fleet"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("commands run = %q, want %q: the answering socket is kept", got, want)
	}
	if requests := socket.Requests(); len(requests) != 2 {
		t.Fatalf("socket got %d requests, want both snapshots", len(requests))
	}
}

// With a cache, a pane input whose status read fails asks Herdr's status once.
func TestSocketCachePaneInputReadsAFailingStatusOnce(t *testing.T) {
	runner := &fakeRunner{replies: []runnerReply{rawReply(`{"server":{"running":false}}`)}}
	var sleeps []time.Duration
	client := newTestClient(runner, &sleeps)
	client.Sockets = NewSocketCache()

	_, err := client.PaneInput(context.Background())

	if err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("PaneInput = %v, want the status's not running error", err)
	}
	if calls := runner.Requests(); len(calls) != 1 {
		t.Fatalf("commands run = %v, want the one status read", calls)
	}
}
