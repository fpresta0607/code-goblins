package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// cfo peek reads a native terminal's screen as its console holds it: the rows
// its program wrote, without the blanks after them, and the last ones when
// fewer are asked for.
func TestPeekReadsANativeTerminalsScreen(t *testing.T) {
	stateDir := t.TempDir()
	hostAttachTestTerminal(t, stateDir, "t1")
	record, err := host.ReadRecord(stateDir, "t1")
	if err != nil {
		t.Fatal(err)
	}
	client, err := host.Dial(record)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.Input([]byte("hello\r")); err != nil {
		t.Fatal(err)
	}
	for shown := ""; !strings.Contains(shown, "got hello"); {
		event, err := client.Next()
		if err != nil || event.Exited {
			t.Fatalf("the terminal ended before it answered: %+v, %v", event, err)
		}
		shown += string(event.Output)
	}
	h := home.Home{Root: filepath.Dir(stateDir), State: stateDir}

	screen, err := peekTerminal(h, "t1", 0)
	last, lastErr := peekTerminal(h, "t1", 1)

	if err != nil || screen != "ready\nhello\ngot hello\n" {
		t.Errorf("peek t1 = %q, %v; want the three rows written", screen, err)
	}
	if lastErr != nil || last != "got hello\n" {
		t.Errorf("peek t1 1 = %q, %v; want the last row written", last, lastErr)
	}
}

func TestPeekAndReadinessReadAnInlineCodexScreen(t *testing.T) {
	stateDir := t.TempDir()
	hostAttachTestTerminal(t, stateDir, "t1")
	if err := state.WriteTaskMeta(stateDir, state.TaskMeta{ID: "t1", Worktree: t.TempDir(), Harness: "codex", Backend: "native"}); err != nil {
		t.Fatal(err)
	}
	record, err := host.ReadRecord(stateDir, "t1")
	if err != nil {
		t.Fatal(err)
	}
	client, err := host.Dial(record)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	if err := client.Input([]byte("codex-inline\r")); err != nil {
		t.Fatal(err)
	}
	screens, ok := harness.NativeScreens(harness.Codex)
	if !ok {
		t.Fatal("Codex has no native readiness detector")
	}
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		rows, err := host.ReadScreen(record)
		if err != nil {
			t.Fatal(err)
		}
		if screens.IsReady(rows) && screens.ComposerEmpty(rows) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("inline Codex screen never became ready: %q", rows)
		}
	}
	h := home.Home{Root: filepath.Dir(stateDir), State: stateDir}
	var stdout, stderr bytes.Buffer
	exit := runPeek([]string{"gb-t1"}, &stdout, &stderr, commandRuntime{
		resolveHome: func() (home.Home, error) { return h, nil },
		peek:        peekTerminal,
	})

	if exit != 0 || stderr.Len() != 0 {
		t.Fatalf("peek exit=%d stderr=%q", exit, stderr.String())
	}
	for _, text := range []string{"Working tree is clean.", "\u203a Ask Codex to do anything", "gpt-6.1-sol high"} {
		if !strings.Contains(stdout.String(), text) {
			t.Errorf("peek = %q, want inline Codex row %q", stdout.String(), text)
		}
	}
}

// cfo peek gb-<id>, the form fleet-view suggests, reads a native task's
// terminal as cfo peek <id> does, never asking Herdr for it.
func TestPeekByTheGoblinsNameReadsANativeTasksTerminal(t *testing.T) {
	stateDir := t.TempDir()
	hostAttachTestTerminal(t, stateDir, "t1")
	if err := state.WriteTaskMeta(stateDir, state.TaskMeta{ID: "t1", Window: "native", Worktree: t.TempDir(), Harness: "claude", Kind: "ship", Backend: "native"}); err != nil {
		t.Fatal(err)
	}
	h := home.Home{Root: filepath.Dir(stateDir), State: stateDir}
	// The terminal's program prints ready once it has started; read it by id
	// until it has, so both names are asked of the same screen.
	byID := ""
	for deadline := time.Now().Add(10 * time.Second); !strings.Contains(byID, "ready"); time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the terminal never showed ready: %q", byID)
		}
		byID, _ = peekTerminal(h, "t1", 0)
	}

	screen, err := peekTerminal(h, "gb-t1", 0)

	if err != nil || screen != byID {
		t.Errorf("peek gb-t1 = %q, %v; want the screen peek t1 reads, %q", screen, err, byID)
	}
}

// cfo peek of a native terminal whose host does not answer fails naming the
// terminal, never showing an empty screen.
func TestPeekOfANativeTerminalWhoseHostDoesNotAnswerFails(t *testing.T) {
	stateDir := t.TempDir()
	record := host.Record{ID: "t1", Pipe: fmt.Sprintf(`\\.\pipe\cfo-peek-test-%d`, time.Now().UnixNano()), Token: "token", Version: host.Version, HostPID: os.Getpid()}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(stateDir, "hosts"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "hosts", "t1.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	screen, err := peekTerminal(home.Home{Root: filepath.Dir(stateDir), State: stateDir}, "t1", 0)

	if err == nil || screen != "" || !strings.Contains(err.Error(), "terminal t1") {
		t.Fatalf("peek t1 = %q, %v; want an error naming terminal t1", screen, err)
	}
}

// cfo peek reads native terminals only: a task an older build recorded in
// Herdr, and a name no terminal answers to, fail naming what was asked for.
func TestPeekOfATaskWithNoNativeTerminalFails(t *testing.T) {
	stateDir := t.TempDir()
	if err := state.WriteTaskMeta(stateDir, state.TaskMeta{ID: "t1", Worktree: t.TempDir(), Harness: "claude", Kind: "ship", Backend: "herdr", HerdrSession: "fleet", HerdrWorkspaceID: "ws", HerdrTabID: "tab-t1", HerdrPaneID: "pane-t1"}); err != nil {
		t.Fatal(err)
	}
	h := home.Home{Root: filepath.Dir(stateDir), State: stateDir}

	for _, target := range []string{"t1", "gb-t1", "fleet:pane-t1", "nobody"} {
		screen, err := peekTerminal(h, target, 0)

		if err == nil || screen != "" || !strings.Contains(err.Error(), target+" has no native terminal running") {
			t.Errorf("peek %s = %q, %v; want a refusal naming it", target, screen, err)
		}
	}
}

func TestRunPeekStreamsOnlyTail(t *testing.T) {
	deps := testCommandRuntime(t)
	var gotTarget string
	var gotLines int
	deps.peek = func(_ home.Home, target string, lines int) (string, error) {
		gotTarget, gotLines = target, lines
		return "marker\n", nil
	}

	var stdout, stderr bytes.Buffer
	exit := runWithRuntime([]string{"peek", "gb-g1", "25"}, &stdout, &stderr, deps)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", exit, stderr.String())
	}
	if gotTarget != "gb-g1" || gotLines != 25 {
		t.Errorf("peek = target %q lines %d, want parsed input", gotTarget, gotLines)
	}
	if stdout.String() != "marker\n" || stderr.Len() != 0 {
		t.Errorf("stdout=%q stderr=%q, want only terminal tail", stdout.String(), stderr.String())
	}
}

func TestRunPeekDefaultsLineCount(t *testing.T) {
	deps := testCommandRuntime(t)
	var gotLines int
	deps.peek = func(_ home.Home, _ string, lines int) (string, error) {
		gotLines = lines
		return "", nil
	}

	var stdout, stderr bytes.Buffer
	exit := runWithRuntime([]string{"peek", "gb-g1"}, &stdout, &stderr, deps)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", exit, stderr.String())
	}
	if gotLines != 0 {
		t.Errorf("lines = %d, want 0, which reads the whole screen", gotLines)
	}
}

func TestRunPeekWritesFailureOnlyToStderr(t *testing.T) {
	deps := testCommandRuntime(t)
	deps.peek = func(home.Home, string, int) (string, error) {
		return "partial", errors.New("pane unavailable")
	}

	var stdout, stderr bytes.Buffer
	exit := runWithRuntime([]string{"peek", "gb-g1"}, &stdout, &stderr, deps)
	if exit != 1 {
		t.Fatalf("exit = %d, want 1", exit)
	}
	if stdout.Len() != 0 || stderr.String() != "pane unavailable\n" {
		t.Errorf("stdout=%q stderr=%q, want diagnostics only", stdout.String(), stderr.String())
	}
}

func TestRunPeekRejectsUnknownFlagInTargetPosition(t *testing.T) {
	deps := testCommandRuntime(t)
	called := false
	deps.peek = func(home.Home, string, int) (string, error) {
		called = true
		return "", nil
	}

	var stdout, stderr bytes.Buffer
	exit := runWithRuntime([]string{"peek", "--unknown"}, &stdout, &stderr, deps)
	if exit != 2 {
		t.Fatalf("exit = %d, want 2; stderr=%s", exit, stderr.String())
	}
	if called {
		t.Fatal("unknown peek flag invoked peeker")
	}
}
