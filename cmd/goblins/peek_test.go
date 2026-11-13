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

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// goblins peek of a native terminal whose host does not answer fails naming the
// terminal, never showing an empty screen.
func TestPeekOfANativeTerminalWhoseHostDoesNotAnswerFails(t *testing.T) {
	stateDir := t.TempDir()
	record := host.Record{ID: "t1", Pipe: fmt.Sprintf(`\\.\pipe\ceo-peek-test-%d`, time.Now().UnixNano()), Token: "token", Version: host.Version, HostPID: os.Getpid()}
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

// goblins peek reads native terminals only: a task an older build recorded in
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
