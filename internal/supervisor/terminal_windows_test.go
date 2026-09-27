package supervisor

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/herdr/herdrtest"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
)

type testTerminal struct {
	frames  chan herdr.TerminalFrame
	closed  chan struct{}
	entered chan struct{}
	block   bool
	once    sync.Once
	writes  []herdr.TerminalCommand
	// control, cols and rows are how the board opened it.
	control    bool
	cols, rows int
	// exited, when set, holds Close until it closes, as a Herdr observer
	// process takes time to exit.
	exited chan struct{}
}

func (s *testTerminal) Next() (herdr.TerminalFrame, error) {
	select {
	case f, ok := <-s.frames:
		if ok {
			return f, nil
		}
		return f, io.EOF
	case <-s.closed:
		return herdr.TerminalFrame{}, io.EOF
	}
}
func (s *testTerminal) Send(c herdr.TerminalCommand) error {
	if s.entered != nil {
		close(s.entered)
	}
	if s.block {
		<-s.closed
		return io.ErrClosedPipe
	}
	s.writes = append(s.writes, c)
	return nil
}
func (s *testTerminal) Close() error {
	s.once.Do(func() { close(s.closed) })
	if s.exited != nil {
		<-s.exited
	}
	return nil
}
func newTestTerminal() *testTerminal {
	return &testTerminal{frames: make(chan herdr.TerminalFrame, 8), closed: make(chan struct{})}
}

func TestTerminalInputOnceGuardAndBoundedWrite(t *testing.T) {
	for _, outcome := range []string{"ok", "changed", "deadline"} {
		t.Run(outcome, func(t *testing.T) {
			native := newTestTerminal()
			defer native.Close()
			native.block = outcome == "deadline"
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			lease := &terminalLease{control: true, stream: native, cancel: func() { _ = native.Close() }}
			guard := func(context.Context, terminalBinding, bool) error {
				if outcome == "changed" {
					return errors.New("changed")
				}
				return nil
			}
			command := herdr.TerminalCommand{Type: "terminal.input", Text: "\x1b[200~one\ntwo\x1b[201~"}
			err := lease.input(ctx, 1, command, guard)
			if outcome == "ok" {
				if err != nil || len(native.writes) != 1 || native.writes[0].Text != command.Text {
					t.Fatal("paste not delivered once", err)
				}
				if lease.input(ctx, 1, command, guard) == nil || len(native.writes) != 1 {
					t.Fatal("duplicate replay")
				}
			} else if err == nil || len(native.writes) > 0 {
				t.Fatal("invalid or blocked input delivered")
			}
		})
	}
}

func terminalHTTPFixture(t *testing.T, native *testTerminal) (*HTTP, *httptest.Server, string, *cfoRunner) {
	t.Helper()
	store, _ := testStore(t)
	_, identity, runner, cfo := primaryFixture(t, store)
	runner.socket = herdrtest.NewSocket(t)
	s := &Service{Store: store, Options: Options{CFO: cfo}, Instance: "instance", done: make(chan struct{})}
	h := NewHTTP(s, "", nil)
	h.openTerminal = func(_ context.Context, session, id string, control bool, cols, rows int) (herdr.TerminalStream, error) {
		if session != "isolated" || id != "test-terminal" {
			t.Error("wrong native terminal", session, id)
		}
		native.control, native.cols, native.rows = control, cols, rows
		return native, nil
	}
	server := httptest.NewServer(h)
	h.Host = strings.TrimPrefix(server.URL, "http://")
	t.Cleanup(server.Close)
	return h, server, identity, runner
}
func terminalPost(t *testing.T, server *httptest.Server, path, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("POST", server.URL+path, strings.NewReader(body))
	req.Header.Set("Origin", server.URL)
	req.Header.Set("X-CFO-Token", "instance")
	req.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

// typedInto is the text of each typing request to pane, in order.
func typedInto(socket *herdrtest.Socket, pane string) []string {
	var typed []string
	for _, request := range socket.Requests() {
		if text, ok := request.Params["text"].(string); ok && request.Method == "pane.send_text" && request.Params["pane_id"] == pane {
			typed = append(typed, text)
		}
	}
	return typed
}
func fullFrame(seq uint64) herdr.TerminalFrame {
	return herdr.TerminalFrame{Type: "terminal.frame", Seq: seq, Encoding: "ansi", Width: 80, Height: 24, Full: true, Bytes: "aGVsbG8="}
}

func TestTerminalFrameGapAndOwnershipReason(t *testing.T) {
	for _, shape := range []string{"gap", "full", "owned"} {
		t.Run(shape, func(t *testing.T) {
			native := newTestTerminal()
			_, server, _, _ := terminalHTTPFixture(t, native)
			native.frames <- fullFrame(1)
			if shape == "owned" {
				native.frames <- herdr.TerminalFrame{Type: "terminal.closed", Reason: "Terminal already has an active controller"}
			} else {
				next := fullFrame(3)
				next.Full = shape == "full"
				native.frames <- next
			}
			close(native.frames)
			response := terminalPost(t, server, "/api/terminal/stream", `{"cols":80,"rows":24}`)
			defer response.Body.Close()
			data, _ := io.ReadAll(response.Body)
			text := string(data)
			if shape == "gap" && !strings.Contains(text, "synchronization") {
				t.Fatal(text)
			}
			if shape == "full" && strings.Contains(text, "synchronization") {
				t.Fatal(text)
			}
			if shape == "owned" && !strings.Contains(text, "active controller") {
				t.Fatal(text)
			}
		})
	}
}

func TestTerminalOutputTerminationUnblocksInput(t *testing.T) {
	native := newTestTerminal()
	native.block = true
	native.entered = make(chan struct{})
	h, server, identity, _ := terminalHTTPFixture(t, native)
	native.frames <- fullFrame(1)
	response := terminalPost(t, server, "/api/terminal/stream", `{"cols":80,"rows":24,"control":true,"identity":"`+identity+`"}`)
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	if !scanner.Scan() {
		t.Fatal("no ready frame")
	}
	var ready struct {
		Lease string `json:"lease"`
	}
	if json.Unmarshal(scanner.Bytes(), &ready) != nil || ready.Lease == "" {
		t.Fatal(string(scanner.Bytes()))
	}
	h.mu.Lock()
	lease := h.terminals[ready.Lease]
	h.mu.Unlock()
	finished := make(chan error, 1)
	go func() {
		finished <- lease.input(context.Background(), 1, herdr.TerminalCommand{Type: "terminal.input", Text: "x"}, h.verifyTerminal)
	}()
	select {
	case <-native.entered:
	case <-time.After(time.Second):
		t.Fatal("write did not start")
	}
	close(native.frames)
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("uncertain write marked delivered")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("output teardown waited on blocked input")
	}
	deadline := time.Now().Add(time.Second)
	for len(h.terminalSlots) != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(h.terminalSlots) != 0 {
		t.Fatal("native slot leaked")
	}
}

func TestNativeBindingRejectsChangedTerminalAndKnownExitedProcess(t *testing.T) {
	native := newTestTerminal()
	h, _, _, runner := terminalHTTPFixture(t, native)
	b, err := h.Service.resolveTerminal(context.Background(), terminalSelection{}, false)
	if err != nil {
		t.Fatal(err)
	}
	runner.terminal = "replacement"
	if h.verifyTerminal(context.Background(), b, true) == nil {
		t.Fatal("changed terminal accepted")
	}
	runner.terminal = ""
	runner.pid = 1
	if h.verifyTerminal(context.Background(), b, true) == nil {
		t.Fatal("exited or changed process accepted")
	}
}

func TestTaskTerminalIdentityIgnoresDeliveryMetadata(t *testing.T) {
	store, h := testStore(t)
	_, _, runner, cfo := primaryFixture(t, store)
	meta, _ := state.ReadTaskMeta(h.State, "task-1")
	meta.Mode = "local-only"
	_ = state.WriteTaskMeta(h.State, meta)
	runner.workerTree = meta.Worktree
	s := &Service{Store: store, Options: Options{CFO: cfo}}
	selected := terminalSelection{Task: meta.ID, Generation: meta.SpawnGen}
	before, err := s.resolveTerminal(context.Background(), selected, true)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(h.State, "task-1.meta")
	data, _ := state.ReadMeta(path)
	data["pr"] = "https://github.com/example/repo/pull/1"
	data["pipeline_hash"] = "new-policy"
	_ = state.WriteMeta(path, data)
	after, err := s.resolveTerminal(context.Background(), selected, true)
	if err != nil || before.Identity != after.Identity {
		t.Fatal("nonrecipient metadata disconnected terminal", err)
	}
	data["spawn_gen"] = "replacement"
	_ = state.WriteMeta(path, data)
	if _, err := s.resolveTerminal(context.Background(), selected, true); err == nil {
		t.Fatal("replacement task accepted")
	}
}

func TestMissingTerminalRegistrationIsNotATransientConnection(t *testing.T) {
	store, h := testStore(t)
	_, _, _, cfo := primaryFixture(t, store)
	meta, _ := state.ReadTaskMeta(h.State, "task-1")
	meta.HerdrPaneID = ""
	meta.Backend = ""
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	handler := NewHTTP(&Service{Store: store, Instance: "instance", Options: Options{CFO: cfo}}, "board.local", nil)
	r := httptest.NewRequest("POST", "http://board.local/api/terminal/stream", strings.NewReader(`{"task":"task-1","generation":"g1","cols":80,"rows":24}`))
	r.Header.Set("Origin", "http://board.local")
	r.Header.Set("X-CFO-Token", "instance")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 409 || !strings.Contains(w.Body.String(), `"code":"terminal_unavailable"`) {
		t.Fatal("missing registration offered retry", w.Code, w.Body.String())
	}
}

// The board observed a pane at the browser's size, and Herdr shows an observer
// only the top-left corner its size covers and nothing that changes below it,
// so a goblin's terminal looked frozen. A view without control observes the
// pane whole, at the size Herdr lays it out.
func TestLivePaneViewShowsThePaneWholeAtItsOwnSize(t *testing.T) {
	for _, c := range []struct {
		name       string
		body       string
		sizeless   bool
		status     int
		control    bool
		cols, rows int
	}{
		{"a view observes the pane at its own size", `{"cols":80,"rows":24}`, false, 200, false, 132, 43},
		{"a view needs no size of its own", `{}`, false, 200, false, 132, 43},
		{"control keeps the size it asked for", `{"cols":80,"rows":24,"control":true,"identity":"IDENTITY"}`, false, 200, true, 80, 24},
		{"a pane with no reported size is refused", `{"cols":80,"rows":24}`, true, 409, false, 0, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			native := newTestTerminal()
			_, server, identity, runner := terminalHTTPFixture(t, native)
			runner.sizeless = c.sizeless
			native.frames <- fullFrame(1)
			close(native.frames)
			response := terminalPost(t, server, "/api/terminal/stream", strings.ReplaceAll(c.body, "IDENTITY", identity))
			defer response.Body.Close()
			data, _ := io.ReadAll(response.Body)
			if response.StatusCode != c.status || native.control != c.control || native.cols != c.cols || native.rows != c.rows {
				t.Fatalf("status %d, opened control=%v %dx%d (%s), want status %d, control=%v %dx%d", response.StatusCode, native.control, native.cols, native.rows, data, c.status, c.control, c.cols, c.rows)
			}
		})
	}
}

// The Overlord types straight into a live view with no Connect step: each
// input is verified again and typed into that same pane over its Herdr
// session's socket, and nothing reaches a pane whose terminal changed.
func TestLivePaneViewTypesIntoItsOwnVerifiedPane(t *testing.T) {
	native := newTestTerminal()
	h, server, _, runner := terminalHTTPFixture(t, native)
	socket := runner.socket
	native.frames <- fullFrame(1)
	response := terminalPost(t, server, "/api/terminal/stream", `{}`)
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	var ready struct {
		Lease   string `json:"lease"`
		Control bool   `json:"control"`
	}
	if !scanner.Scan() || json.Unmarshal(scanner.Bytes(), &ready) != nil || ready.Lease == "" || ready.Control {
		t.Fatalf("no live view lease: %s", scanner.Text())
	}
	send := func(seq int, command string) int {
		t.Helper()
		reply := terminalPost(t, server, "/api/terminal/input", fmt.Sprintf(`{"lease":%q,"seq":%d,"command":%s}`, ready.Lease, seq, command))
		defer reply.Body.Close()
		return reply.StatusCode
	}
	if status := send(1, `{"type":"terminal.input","text":"echo hi\r"}`); status != 200 {
		t.Fatalf("typing = %d, want 200", status)
	}
	// Dash-leading text is typed as it is.
	if status := send(2, `{"type":"terminal.input","text":"--help"}`); status != 200 {
		t.Fatalf("typing --help = %d, want 200", status)
	}
	if got, want := typedInto(socket, "w1:p1"), []string{"echo hi\r", "--help"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("typed %q, want %q", got, want)
	}
	if status := send(3, `{"type":"terminal.resize","cols":100,"rows":30}`); status != 409 {
		t.Fatalf("resizing a live view = %d, want 409", status)
	}
	// A re-registered CFO is a changed recipient, and the next key sees it in
	// the registration file without asking Herdr.
	reregister(t, h.Service.Store.Home.State)
	if status := send(3, `{"type":"terminal.input","text":"x"}`); status != 409 || len(typedInto(socket, "w1:p1")) != 2 {
		t.Fatalf("typing after the CFO re-registered = %d with %d typed, want 409 and nothing more typed", status, len(typedInto(socket, "w1:p1")))
	}
	if len(native.writes) != 0 {
		t.Fatalf("typing went through the observer: %v", native.writes)
	}
}

// A live view is opened at the pane's size, so a pane that grows afterwards
// would again show only its old top-left corner; the view ends instead, and
// the board reconnects at the new size.
func TestLivePaneViewEndsWhenThePaneIsResized(t *testing.T) {
	native := newTestTerminal()
	h, server, _, runner := terminalHTTPFixture(t, native)
	h.terminalTick = 10 * time.Millisecond
	native.frames <- fullFrame(1)
	response := terminalPost(t, server, "/api/terminal/stream", `{}`)
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	var frame herdr.TerminalFrame
	for frames := 0; frame.Type != "terminal.closed"; frames++ {
		if frames > 100 {
			t.Fatal("the view went on after its pane was resized")
		}
		if !scanner.Scan() || json.Unmarshal(scanner.Bytes(), &frame) != nil {
			t.Fatalf("view ended without a reason: %s", scanner.Text())
		}
		runner.resized.Store(true)
	}
	if native.cols != 132 || native.rows != 43 || frame.Reason != "The pane was resized. Reconnect to see it whole at its new size." {
		t.Fatalf("opened %dx%d and ended with %q, want 132x43 ended as resized", native.cols, native.rows, frame.Reason)
	}
}

// Once a view reports terminal.closed, typing into it is refused even while
// its observer process is still exiting.
func TestLivePaneViewRefusesTypingOnceItReportsClosed(t *testing.T) {
	native := newTestTerminal()
	native.exited = make(chan struct{})
	defer close(native.exited)
	h, server, _, runner := terminalHTTPFixture(t, native)
	socket := runner.socket
	h.terminalTick = 10 * time.Millisecond
	native.frames <- fullFrame(1)
	response := terminalPost(t, server, "/api/terminal/stream", `{}`)
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	var ready struct {
		Lease string `json:"lease"`
	}
	if !scanner.Scan() || json.Unmarshal(scanner.Bytes(), &ready) != nil || ready.Lease == "" {
		t.Fatalf("no live view lease: %s", scanner.Text())
	}
	runner.resized.Store(true)
	for frame := (herdr.TerminalFrame{}); frame.Type != "terminal.closed"; {
		if !scanner.Scan() || json.Unmarshal(scanner.Bytes(), &frame) != nil {
			t.Fatalf("view ended without a reason: %s", scanner.Text())
		}
	}
	reply := terminalPost(t, server, "/api/terminal/input", fmt.Sprintf(`{"lease":%q,"seq":1,"command":{"type":"terminal.input","text":"x"}}`, ready.Lease))
	defer reply.Body.Close()
	if reply.StatusCode != 409 || len(socket.Requests()) != 0 {
		t.Fatalf("typing after terminal.closed = %d with %q typed, want 409 and nothing typed", reply.StatusCode, typedInto(socket, "w1:p1"))
	}
}

// A socket request carries any input the board accepts, so a long paste is
// typed whole in one request, and a NUL key such as Ctrl+Space is typed as
// it is; a request Herdr refuses ends the view instead of typing the rest.
func TestLivePaneViewTypesALargePasteInOneRequest(t *testing.T) {
	panes := &recordingPanes{}
	allow := func(context.Context, terminalBinding, bool) error { return nil }
	inputs := []string{strings.Repeat("ab日", 3000), "a\x00"}
	lease := &terminalLease{panes: panes, cancel: func() {}}
	for seq, text := range inputs {
		if err := lease.input(context.Background(), uint64(seq+1), herdr.TerminalCommand{Type: "terminal.input", Text: text}, allow); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(panes.typed, inputs) {
		t.Fatalf("typed %d requests, want the paste and the NUL key whole, one request each", len(panes.typed))
	}

	refused := &terminalLease{panes: &recordingPanes{refuse: 1}, cancel: func() {}}
	if err := refused.input(context.Background(), 1, herdr.TerminalCommand{Type: "terminal.input", Text: "x"}, allow); err == nil || !strings.Contains(err.Error(), "outcome is unknown") {
		t.Fatalf("a refused input = %v, want an unknown outcome", err)
	}
	if err := refused.input(context.Background(), 2, herdr.TerminalCommand{Type: "terminal.input", Text: "y"}, allow); err == nil || len(refused.panes.(*recordingPanes).typed) != 0 {
		t.Fatalf("after an input with an unknown outcome = %v, want the view closed and nothing more typed", err)
	}
}

// The wheel shows the pane's history, which a Herdr observer never sends: the
// view reads it from the pane over the socket, the lines it asked for, and a
// read types nothing. A view that ended, or whose pane changed, reads
// nothing.
func TestLivePaneViewReadsThePanesHistory(t *testing.T) {
	// Arrange
	panes := &recordingPanes{text: "line 1\nline 2"}
	allow := func(context.Context, terminalBinding, bool) error { return nil }
	lease := &terminalLease{binding: terminalBinding{Target: herdr.Target{Session: "isolated", Pane: "w1:p1"}}, panes: panes, cancel: func() {}}

	// Act
	text, err := lease.history(context.Background(), 2000, allow)

	// Assert
	if err != nil || text != "line 1\nline 2" {
		t.Fatalf("history = %q, %v; want the pane's text", text, err)
	}
	if !reflect.DeepEqual(panes.reads, []string{"w1:p1 2000"}) || len(panes.typed) != 0 {
		t.Fatalf("read %v and typed %q, want one read of 2000 lines of w1:p1 and nothing typed", panes.reads, panes.typed)
	}

	changed := func(context.Context, terminalBinding, bool) error {
		return errors.New("Native terminal identity changed.")
	}
	if _, err := lease.history(context.Background(), 10, changed); err == nil || len(panes.reads) != 1 {
		t.Fatalf("history of a changed pane = %v with %d reads, want refused unread", err, len(panes.reads))
	}
	lease.closed = true
	if _, err := lease.history(context.Background(), 10, allow); err == nil || len(panes.reads) != 1 {
		t.Fatalf("history of an ended view = %v with %d reads, want refused unread", err, len(panes.reads))
	}
}

// A history read can take seconds, and a key typed meanwhile does not wait
// for it.
func TestLivePaneViewTypesWhileItsHistoryIsRead(t *testing.T) {
	// Arrange
	panes := &recordingPanes{reading: make(chan struct{}), release: make(chan struct{})}
	allow := func(context.Context, terminalBinding, bool) error { return nil }
	lease := &terminalLease{panes: panes, cancel: func() {}}
	read := make(chan error, 1)
	go func() {
		_, err := lease.history(context.Background(), 10, allow)
		read <- err
	}()
	<-panes.reading

	// Act
	typed := make(chan error, 1)
	go func() {
		typed <- lease.input(context.Background(), 1, herdr.TerminalCommand{Type: "terminal.input", Text: "x"}, allow)
	}()

	// Assert
	select {
	case err := <-typed:
		if err != nil || !reflect.DeepEqual(panes.typed, []string{"x"}) {
			t.Fatalf("a key during a history read = %v, typed %q; want x typed", err, panes.typed)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a key waited behind a history read")
	}
	close(panes.release)
	if err := <-read; err != nil {
		t.Fatalf("history = %v, want read", err)
	}
}

// A live view sends only typing: scrolling Herdr's pane would move every
// Herdr window on it and still not reach the view, so a scroll is refused
// and nothing reaches the pane.
func TestLivePaneViewRefusesToScrollThePane(t *testing.T) {
	panes := &recordingPanes{}
	allow := func(context.Context, terminalBinding, bool) error { return nil }
	lease := &terminalLease{panes: panes, cancel: func() {}}

	err := lease.input(context.Background(), 1, herdr.TerminalCommand{Type: "terminal.scroll", Direction: "up", Lines: 3, Source: "wheel"}, allow)

	if err == nil || len(panes.typed) != 0 || len(panes.reads) != 0 {
		t.Fatalf("a scroll = %v, want refused with nothing sent to the pane", err)
	}
}

// The history request reads the pane the lease is bound to, bounded in
// lines, and answers its text; an unknown lease or a line count out of
// bounds reads nothing.
func TestTerminalHistoryAnswersThePanesRecentLines(t *testing.T) {
	native := newTestTerminal()
	_, server, _, runner := terminalHTTPFixture(t, native)
	socket := runner.socket
	socket.History = []string{"first", "second", "\x1b[31mthird\x1b[0m"}
	native.frames <- fullFrame(1)
	response := terminalPost(t, server, "/api/terminal/stream", `{}`)
	defer response.Body.Close()
	lease := readyLease(t, bufio.NewScanner(response.Body))
	cases := []struct {
		name   string
		body   string
		status int
		text   string
	}{
		{"the last lines asked for", fmt.Sprintf(`{"lease":%q,"lines":2}`, lease), 200, "second\n\x1b[31mthird\x1b[0m"},
		{"all of a shorter history", fmt.Sprintf(`{"lease":%q,"lines":5000}`, lease), 200, "first\nsecond\n\x1b[31mthird\x1b[0m"},
		{"no lines", fmt.Sprintf(`{"lease":%q,"lines":0}`, lease), 400, ""},
		{"too many lines", fmt.Sprintf(`{"lease":%q,"lines":5001}`, lease), 400, ""},
		{"an unknown lease", `{"lease":"gone","lines":10}`, 409, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before := len(socket.Requests())

			reply := terminalPost(t, server, "/api/terminal/history", c.body)
			defer reply.Body.Close()
			var answer struct {
				Text string `json:"text"`
			}
			_ = json.NewDecoder(reply.Body).Decode(&answer)

			reads := socket.Requests()[before:]
			if reply.StatusCode != c.status || answer.Text != c.text {
				t.Fatalf("history = %d %q, want %d %q", reply.StatusCode, answer.Text, c.status, c.text)
			}
			if c.status == 200 && (len(reads) != 1 || reads[0].Method != "pane.read" || reads[0].Params["pane_id"] != "w1:p1" || reads[0].Params["source"] != "recent" || reads[0].Params["format"] != "ansi") {
				t.Fatalf("requests = %v, want one pane.read of w1:p1's recent ANSI output", reads)
			}
			if c.status != 200 && len(reads) != 0 {
				t.Fatalf("a refused history request sent %v", reads)
			}
		})
	}
}

// Taking a pane's control to size it to the board must never resume or answer
// an agent waiting for input: opening a control view, resizing it and
// reading its history send the pane nothing but its size, and only a key the
// Overlord types reaches the program.
func TestTakingControlSendsThePaneOnlyItsSize(t *testing.T) {
	// Arrange
	native := newTestTerminal()
	_, server, identity, runner := terminalHTTPFixture(t, native)
	socket := runner.socket
	socket.History = []string{"waiting for your answer"}
	native.frames <- fullFrame(1)

	// Act
	response := terminalPost(t, server, "/api/terminal/stream", `{"cols":100,"rows":30,"control":true,"identity":"`+identity+`"}`)
	defer response.Body.Close()
	lease := readyLease(t, bufio.NewScanner(response.Body))
	resized := terminalPost(t, server, "/api/terminal/input", fmt.Sprintf(`{"lease":%q,"seq":1,"command":{"type":"terminal.resize","cols":90,"rows":25}}`, lease))
	resized.Body.Close()
	history := terminalPost(t, server, "/api/terminal/history", fmt.Sprintf(`{"lease":%q,"lines":100}`, lease))
	history.Body.Close()

	// Assert
	if !native.control || native.cols != 100 || native.rows != 30 {
		t.Fatalf("opened control=%v at %dx%d, want control at 100x30", native.control, native.cols, native.rows)
	}
	if resized.StatusCode != 200 || history.StatusCode != 200 {
		t.Fatalf("resize = %d, history = %d, want 200 and 200", resized.StatusCode, history.StatusCode)
	}
	if want := []herdr.TerminalCommand{{Type: "terminal.resize", Cols: 90, Rows: 25}}; !reflect.DeepEqual(native.writes, want) {
		t.Fatalf("sent the pane %v, want only its new size", native.writes)
	}
	for _, request := range socket.Requests() {
		if request.Method != "pane.read" {
			t.Fatalf("the socket was sent %s %v, want only history reads", request.Method, request.Params)
		}
	}
	if status := typeKey(t, server, lease, 2, "y"); status != 200 || !reflect.DeepEqual(native.writes[len(native.writes)-1], herdr.TerminalCommand{Type: "terminal.input", Text: "y"}) {
		t.Fatalf("a typed key = %d with %v sent, want it typed", status, native.writes)
	}
}

// A pane the board sized keeps that size once the board's view of it ends
// unless the board hands it back, since with no Herdr window open nothing
// else would: a control view's end, however it ends, returns the pane to the
// size Herdr lays it out at, and a view that only observes sends nothing.
func TestAViewEndingHandsBackOnlyTheSizeItTook(t *testing.T) {
	for _, c := range []struct {
		name string
		body string
		want []herdr.TerminalCommand
	}{
		{"a control view returns the pane to its layout size", `{"cols":100,"rows":30,"control":true,"identity":"IDENTITY"}`, []herdr.TerminalCommand{{Type: "terminal.resize", Cols: 132, Rows: 43}}},
		{"an observer view sends nothing", `{}`, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			native := newTestTerminal()
			_, server, identity, _ := terminalHTTPFixture(t, native)
			native.frames <- fullFrame(1)
			response := terminalPost(t, server, "/api/terminal/stream", strings.ReplaceAll(c.body, "IDENTITY", identity))
			readyLease(t, bufio.NewScanner(response.Body))

			// Act
			response.Body.Close()
			waitClosed(t, native)

			// Assert
			if !reflect.DeepEqual(native.writes, c.want) {
				t.Fatalf("the ended view sent the pane %v, want %v", native.writes, c.want)
			}
		})
	}
}

// A gate that takes a goblin over while the board sizes its pane would refuse
// the view's next size and break it, so the tick ends the sizing view with the
// reason and the pane gets its own size back; the board then shows it at that
// size.
func TestControlViewEndsWhenAGateTakesOverOnItsTick(t *testing.T) {
	// Arrange
	native := newTestTerminal()
	h, server, _, runner := terminalHTTPFixture(t, native)
	body := goblinView(t, h, runner)
	var selected terminalSelection
	if err := json.Unmarshal([]byte(body), &selected); err != nil {
		t.Fatal(err)
	}
	b, err := h.Service.resolveTerminal(context.Background(), selected, false)
	if err != nil {
		t.Fatal(err)
	}
	taken := &atomic.Bool{}
	h.Service.Options.Gate = gateTakesOver{taken: taken}
	h.terminalTick = 10 * time.Millisecond
	native.frames <- fullFrame(1)
	response := terminalPost(t, server, "/api/terminal/stream", strings.TrimSuffix(body, "}")+fmt.Sprintf(`,"control":true,"identity":%q,"cols":100,"rows":30}`, b.Identity))
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	readyLease(t, scanner)

	// Act
	taken.Store(true)
	var frame herdr.TerminalFrame
	for frame.Type != "terminal.closed" {
		if !scanner.Scan() || json.Unmarshal(scanner.Bytes(), &frame) != nil {
			t.Fatalf("the view ended without a reason: %s", scanner.Text())
		}
	}
	waitClosed(t, native)

	// Assert
	if frame.Reason != "A review gate owns this goblin's work now, so the board shows its pane at its own size." {
		t.Fatalf("the view ended with %q, want the gate's reason", frame.Reason)
	}
	if want := []herdr.TerminalCommand{{Type: "terminal.resize", Cols: 132, Rows: 43}}; !reflect.DeepEqual(native.writes, want) {
		t.Fatalf("the ended view sent the pane %v, want its layout size", native.writes)
	}
}

// waitClosed waits for the view to close its Herdr stream, which it does last.
func waitClosed(t *testing.T, native *testTerminal) {
	t.Helper()
	select {
	case <-native.closed:
	case <-time.After(10 * time.Second):
		t.Fatal("the view never closed its Herdr stream")
	}
}

// recordingPanes is a pane input that records what it types and which
// history it reads, answering text; refuse fails that many inputs first.
type recordingPanes struct {
	text   string
	refuse int
	typed  []string
	reads  []string
	// reading, when set, is closed as a history read starts, which then
	// waits for release.
	reading, release chan struct{}
}

func (p *recordingPanes) SendText(_ context.Context, _ string, text string) error {
	if p.refuse > 0 {
		p.refuse--
		return errors.New("pane busy")
	}
	p.typed = append(p.typed, text)
	return nil
}

func (p *recordingPanes) History(_ context.Context, pane string, lines int) (string, error) {
	p.reads = append(p.reads, fmt.Sprintf("%s %d", pane, lines))
	if p.reading != nil {
		close(p.reading)
		<-p.release
	}
	return p.text, nil
}

// Each key is typed without starting a process: the view proved its pane
// and found its Herdr socket when it opened, and a key only rereads local
// files and makes one socket request, so fifty keys run no Herdr command.
func TestLivePaneViewStartsNoProcessPerKey(t *testing.T) {
	native := newTestTerminal()
	_, server, _, runner := terminalHTTPFixture(t, native)
	socket := runner.socket
	native.frames <- fullFrame(1)
	response := terminalPost(t, server, "/api/terminal/stream", `{}`)
	defer response.Body.Close()
	lease := readyLease(t, bufio.NewScanner(response.Body))
	opened := runner.calls
	for seq := 1; seq <= 50; seq++ {
		if status := typeKey(t, server, lease, seq, "x"); status != 200 {
			t.Fatalf("key %d = %d, want 200", seq, status)
		}
	}
	if calls, typed := runner.calls-opened, len(typedInto(socket, "w1:p1")); calls != 0 || typed != 50 {
		t.Fatalf("fifty keys ran %d Herdr commands and typed %d, want no command and fifty socket requests", calls, typed)
	}
}

// A live view whose Herdr socket cannot be found is refused before it opens,
// since it could not type, while a control lease never looks for the socket.
func TestLivePaneViewWithoutItsHerdrSocketIsRefused(t *testing.T) {
	for _, c := range []struct {
		name   string
		body   string
		status int
		reason string
	}{
		{"a live view is refused", `{}`, 503, "Typing into this pane is unavailable: its Herdr socket cannot be found."},
		{"a control lease still opens", `{"cols":80,"rows":24,"control":true,"identity":"IDENTITY"}`, 200, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			native := newTestTerminal()
			_, server, identity, runner := terminalHTTPFixture(t, native)
			runner.socket = nil
			native.frames <- fullFrame(1)
			close(native.frames)

			response := terminalPost(t, server, "/api/terminal/stream", strings.ReplaceAll(c.body, "IDENTITY", identity))
			defer response.Body.Close()
			data, _ := io.ReadAll(response.Body)

			if response.StatusCode != c.status || !strings.Contains(string(data), c.reason) {
				t.Fatalf("status %d (%s), want %d naming %q", response.StatusCode, data, c.status, c.reason)
			}
			if c.status == 503 && native.cols != 0 {
				t.Fatalf("a refused view opened the observer at %dx%d", native.cols, native.rows)
			}
		})
	}
}

// A view's tick proves its pane again with Herdr commands, which take
// seconds on a loaded machine; the screen goes on meanwhile, so an echo is
// never held behind a tick.
func TestLivePaneViewForwardsFramesWhileItsTickChecks(t *testing.T) {
	native := newTestTerminal()
	h, server, _, runner := terminalHTTPFixture(t, native)
	runner.held, runner.release = make(chan struct{}, 1), make(chan struct{})
	t.Cleanup(func() { close(runner.release) })
	h.terminalTick = 20 * time.Millisecond
	native.frames <- fullFrame(1)
	response := terminalPost(t, server, "/api/terminal/stream", `{}`)
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	readyLease(t, scanner)
	runner.holding.Store(true)
	select {
	case <-runner.held:
	case <-time.After(5 * time.Second):
		t.Fatal("no tick checked the pane")
	}

	native.frames <- herdr.TerminalFrame{Type: "terminal.frame", Seq: 2, Encoding: "ansi", Width: 80, Height: 24, Bytes: "aGk="}

	echoed := make(chan bool, 1)
	go func() {
		for scanner.Scan() {
			if strings.Contains(scanner.Text(), `"seq":2`) {
				echoed <- true
				return
			}
		}
		echoed <- false
	}()
	select {
	case ok := <-echoed:
		if !ok {
			t.Fatal("the view ended before the frame came")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a frame waited behind the tick's Herdr commands")
	}
}

// A key rereads only local state, so an unchanged binding costs no Herdr call,
// while an exited process, a restarted task and a re-registered CFO each fall
// through to the full verification, which refuses them.
func TestEachKeyVerifiesInFullOnlyWhenItsBindingChanged(t *testing.T) {
	native := newTestTerminal()
	h, _, _, runner := terminalHTTPFixture(t, native)
	ctx := context.Background()
	primary, err := h.Service.resolveTerminal(ctx, terminalSelection{}, true)
	if err != nil {
		t.Fatal(err)
	}
	before := runner.calls
	if err := h.stillBound(ctx, primary, true); err != nil || runner.calls != before {
		t.Fatalf("an unchanged binding = %v after %d Herdr calls, want accepted without any", err, runner.calls-before)
	}
	exited := primary
	exited.Process.PID = 1
	if h.stillBound(ctx, exited, true) == nil {
		t.Fatal("a key for an exited process was accepted")
	}

	meta, err := state.ReadTaskMeta(h.Service.Store.Home.State, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	meta.Mode = "local-only"
	if err := state.WriteTaskMeta(h.Service.Store.Home.State, meta); err != nil {
		t.Fatal(err)
	}
	runner.workerTree = meta.Worktree
	task, err := h.Service.resolveTerminal(ctx, terminalSelection{Task: meta.ID, Generation: meta.SpawnGen}, true)
	if err != nil {
		t.Fatal(err)
	}
	before = runner.calls
	if err := h.stillBound(ctx, task, true); err != nil || runner.calls != before {
		t.Fatalf("an unchanged task binding = %v after %d Herdr calls, want accepted without any", err, runner.calls-before)
	}
	path := filepath.Join(h.Service.Store.Home.State, "task-1.meta")
	data, err := state.ReadMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	data["spawn_gen"] = "replacement"
	if err := state.WriteMeta(path, data); err != nil {
		t.Fatal(err)
	}
	if h.stillBound(ctx, task, true) == nil {
		t.Fatal("a key for a restarted task was accepted")
	}

	reregister(t, h.Service.Store.Home.State)
	if h.stillBound(ctx, primary, true) == nil {
		t.Fatal("a key for a re-registered CFO was accepted")
	}
}

// Custody is proved when a view opens rather than per key: a live view of a
// goblin whose gate owns the pane still opens and reads its history, which
// reaches no program, over the socket, and a key is refused with nothing
// typed.
func TestLivePaneViewRefusesTypingWhileAGateOwnsThePane(t *testing.T) {
	native := newTestTerminal()
	h, server, _, runner := terminalHTTPFixture(t, native)
	socket := runner.socket
	socket.History = []string{"built", "tested"}
	body := goblinView(t, h, runner)
	h.Service.Options.Gate = fakeProgress{value: pipeline.Progress{Status: "running"}}
	native.frames <- fullFrame(1)
	response := terminalPost(t, server, "/api/terminal/stream", body)
	defer response.Body.Close()
	lease := readyLease(t, bufio.NewScanner(response.Body))
	opened := runner.calls
	history := terminalPost(t, server, "/api/terminal/history", fmt.Sprintf(`{"lease":%q,"lines":100}`, lease))
	history.Body.Close()
	requests := socket.Requests()
	if history.StatusCode != 200 || len(requests) != 1 || requests[0].Method != "pane.read" || runner.calls != opened {
		t.Fatalf("reading history under a gate = %d with %v and %d Herdr commands, want 200, one pane.read on the socket and no command", history.StatusCode, requests, runner.calls-opened)
	}
	if status := typeKey(t, server, lease, 1, "x"); status != 409 || len(typedInto(socket, "w1:p1")) != 0 {
		t.Fatalf("typing under a gate = %d with %d typed, want 409 and nothing typed", status, len(typedInto(socket, "w1:p1")))
	}
}

// Custody is proved again on every tick, so a gate that takes a goblin over
// after its view opened refuses the next key once a tick has seen it.
func TestLivePaneViewNoticesAGateTakingOverOnItsTick(t *testing.T) {
	native := newTestTerminal()
	h, server, _, runner := terminalHTTPFixture(t, native)
	socket := runner.socket
	body := goblinView(t, h, runner)
	taken := &atomic.Bool{}
	h.Service.Options.Gate = gateTakesOver{taken: taken}
	h.terminalTick = 10 * time.Millisecond
	native.frames <- fullFrame(1)
	response := terminalPost(t, server, "/api/terminal/stream", body)
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	lease := readyLease(t, scanner)
	if status := typeKey(t, server, lease, 1, "x"); status != 200 {
		t.Fatalf("typing before the gate = %d, want 200", status)
	}
	taken.Store(true)
	for alive := 0; alive < 2; {
		if !scanner.Scan() {
			t.Fatal("the view ended before a tick saw the gate")
		}
		if strings.Contains(scanner.Text(), `"terminal.alive"`) {
			alive++
		}
	}
	if status := typeKey(t, server, lease, 2, "y"); status != 409 || len(typedInto(socket, "p1")) != 1 {
		t.Fatalf("typing after the gate took over = %d with %d typed, want 409 and nothing more typed", status, len(typedInto(socket, "p1")))
	}
}

// gateTakesOver has no run for the branch until taken is set, and then owns
// the task, as a gate a goblin starts while the Overlord watches its pane.
type gateTakesOver struct{ taken *atomic.Bool }

func (g gateTakesOver) Progress(context.Context, string, string) (pipeline.Progress, error) {
	return pipeline.Progress{}, pipeline.ErrNoProgress
}

func (g gateTakesOver) CanSteer(context.Context, string, string, string) error {
	if g.taken.Load() {
		return errors.New("pipeline owns this task; use cfo pipeline respond or inspect its delivery evidence")
	}
	return nil
}

// goblinView gives task-1 a git worktree the custody check can read and a pane
// the fake Herdr shows, and returns the body that opens its live view.
func goblinView(t *testing.T, h *HTTP, runner *cfoRunner) string {
	t.Helper()
	meta, err := state.ReadTaskMeta(h.Service.Store.Home.State, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	meta.HerdrSession = "isolated"
	if err := state.WriteTaskMeta(h.Service.Store.Home.State, meta); err != nil {
		t.Fatal(err)
	}
	gitFixture(t, meta.Worktree)
	runner.workerTree = meta.Worktree
	return fmt.Sprintf(`{"task":%q,"generation":%q}`, meta.ID, meta.SpawnGen)
}

func readyLease(t *testing.T, scanner *bufio.Scanner) string {
	t.Helper()
	var ready struct {
		Lease string `json:"lease"`
	}
	if !scanner.Scan() || json.Unmarshal(scanner.Bytes(), &ready) != nil || ready.Lease == "" {
		t.Fatalf("no live view lease: %s", scanner.Text())
	}
	return ready.Lease
}

func typeKey(t *testing.T, server *httptest.Server, lease string, seq int, text string) int {
	t.Helper()
	reply := terminalPost(t, server, "/api/terminal/input", fmt.Sprintf(`{"lease":%q,"seq":%d,"command":{"type":"terminal.input","text":%q}}`, lease, seq, text))
	defer reply.Body.Close()
	return reply.StatusCode
}

// reregister rewrites the CFO's registration as a new registration in another
// tab would, which changes its identity.
func reregister(t *testing.T, stateDir string) {
	t.Helper()
	path := filepath.Join(stateDir, "primary.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var primary primaryRegistration
	if err := json.Unmarshal(data, &primary); err != nil {
		t.Fatal(err)
	}
	primary.Tab = "w1:t2"
	if data, err = json.Marshal(primary); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
