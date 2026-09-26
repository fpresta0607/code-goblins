package herdr

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/herdr/herdrtest"
)

// A typist asks Herdr's status for the socket once, then types every input as
// one pane.send_text request on the session's socket: control keys, a
// multi-byte paste and the board's largest input alike, and no process.
func TestTypistTypesEachInputAsOneSocketRequest(t *testing.T) {
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

	typist, err := client.Typist(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range inputs {
		if err := typist(context.Background(), Target{Session: "fleet", Pane: "w1:p2"}, input.text); err != nil {
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
			t.Fatalf("request for %s = %s to %q with %d bytes, want pane.send_text to w1:p2 with the input", input.name, got.Method, got.Params["pane_id"], len(got.Params["text"]))
		}
	}
	if calls := runner.Requests(); len(calls) != 1 || strings.Join(calls[0].Args, " ") != "status --json --session fleet" {
		t.Fatalf("commands run = %v, want only the one status read", calls)
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
