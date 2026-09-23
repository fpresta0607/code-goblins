package supervisor

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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
	h.openTerminal = func(_ context.Context, session, id string, _ bool, _, _ int) (herdr.TerminalStream, error) {
		if session != "isolated" || id != "test-terminal" {
			t.Error("wrong native terminal", session, id)
		}
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
