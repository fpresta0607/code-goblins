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
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/fpresta0607/code-goblins/internal/herdr"
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
func (s *testTerminal) Close() error { s.once.Do(func() { close(s.closed) }); return nil }
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
// input is verified again and typed into that same pane with Herdr's pane
// send-text, as cfo send types into a pane, and nothing reaches a pane whose
// terminal changed.
func TestLivePaneViewTypesIntoItsOwnVerifiedPane(t *testing.T) {
	native := newTestTerminal()
	_, server, _, runner := terminalHTTPFixture(t, native)
	runner.typing = true
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
	// Herdr 0.9 types dash-leading text literally, and a -- separator would be typed too.
	if status := send(2, `{"type":"terminal.input","text":"--help"}`); status != 200 {
		t.Fatalf("typing --help = %d, want 200", status)
	}
	if want := [][]string{{"pane", "send-text", "w1:p1", "echo hi\r", "--session", "isolated"}, {"pane", "send-text", "w1:p1", "--help", "--session", "isolated"}}; !reflect.DeepEqual(runner.typed, want) {
		t.Fatalf("typed %q, want %q", runner.typed, want)
	}
	if status := send(3, `{"type":"terminal.resize","cols":100,"rows":30}`); status != 409 {
		t.Fatalf("resizing a live view = %d, want 409", status)
	}
	runner.terminal = "replacement"
	if status := send(3, `{"type":"terminal.input","text":"x"}`); status != 409 || len(runner.typed) != 2 {
		t.Fatalf("typing after the terminal changed = %d with %d typed, want 409 and nothing more typed", status, len(runner.typed))
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

// A paste longer than one command line can carry is typed in order, in
// pieces; a piece Herdr refuses ends the view instead of typing the rest, and
// a NUL key, which no command line can carry, is refused before anything.
func TestLivePaneViewTypesALargePasteInOrderedPieces(t *testing.T) {
	var pieces []string
	typist := func(_ context.Context, _ herdr.Target, piece string) error {
		pieces = append(pieces, piece)
		return nil
	}
	allow := func(context.Context, terminalBinding, bool) error { return nil }
	text := strings.Repeat("ab日", 3000)
	lease := &terminalLease{typist: typist, cancel: func() {}}
	if err := lease.input(context.Background(), 1, herdr.TerminalCommand{Type: "terminal.input", Text: text}, allow); err != nil {
		t.Fatal(err)
	}
	if len(pieces) != 3 || strings.Join(pieces, "") != text {
		t.Fatalf("typed %d pieces, want the paste whole and in order in 3", len(pieces))
	}
	for _, piece := range pieces {
		if utf8.RuneCountInString(piece) > typedPiece {
			t.Fatalf("a piece of %d runes is longer than a command line carries", utf8.RuneCountInString(piece))
		}
	}
	if err := lease.input(context.Background(), 2, herdr.TerminalCommand{Type: "terminal.input", Text: "a\x00"}, allow); err == nil || len(pieces) != 3 {
		t.Fatalf("a NUL key = %v with %d pieces, want it refused and nothing typed", err, len(pieces))
	}

	attempts := 0
	refusedOnce := func(context.Context, herdr.Target, string) error {
		attempts++
		if attempts == 1 {
			return errors.New("pane busy")
		}
		return nil
	}
	refused := &terminalLease{typist: refusedOnce, cancel: func() {}}
	if err := refused.input(context.Background(), 1, herdr.TerminalCommand{Type: "terminal.input", Text: "x"}, allow); err == nil || !strings.Contains(err.Error(), "outcome is unknown") {
		t.Fatalf("a refused piece = %v, want an unknown outcome", err)
	}
	if err := refused.input(context.Background(), 2, herdr.TerminalCommand{Type: "terminal.input", Text: "y"}, allow); err == nil || attempts != 1 {
		t.Fatalf("after a piece with an unknown outcome = %v with %d attempts, want the view closed and nothing more typed", err, attempts)
	}
}
