package main

import (
	"bytes"
	"context"
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

	screen, err := peekTerminal(context.Background(), h, "t1", 0)
	last, lastErr := peekTerminal(context.Background(), h, "t1", 1)

	if err != nil || screen != "ready\nhello\ngot hello\n" {
		t.Errorf("peek t1 = %q, %v; want the three rows written", screen, err)
	}
	if lastErr != nil || last != "got hello\n" {
		t.Errorf("peek t1 1 = %q, %v; want the last row written", last, lastErr)
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

	screen, err := peekTerminal(context.Background(), home.Home{Root: filepath.Dir(stateDir), State: stateDir}, "t1", 0)

	if err == nil || screen != "" || !strings.Contains(err.Error(), "terminal t1") {
		t.Fatalf("peek t1 = %q, %v; want an error naming terminal t1", screen, err)
	}
}

func TestRunPeekStreamsOnlyTail(t *testing.T) {
	deps := testCommandRuntime(t)
	var gotTarget string
	var gotLines int
	deps.peek = func(_ context.Context, _ home.Home, target string, lines int) (string, error) {
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
	deps.peek = func(_ context.Context, _ home.Home, _ string, lines int) (string, error) {
		gotLines = lines
		return "", nil
	}

	var stdout, stderr bytes.Buffer
	exit := runWithRuntime([]string{"peek", "gb-g1"}, &stdout, &stderr, deps)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", exit, stderr.String())
	}
	if gotLines != 0 {
		t.Errorf("lines = %d, want 0 so fleet.Peeker selects its default", gotLines)
	}
}

func TestRunPeekWritesFailureOnlyToStderr(t *testing.T) {
	deps := testCommandRuntime(t)
	deps.peek = func(context.Context, home.Home, string, int) (string, error) {
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
	deps.peek = func(context.Context, home.Home, string, int) (string, error) {
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
