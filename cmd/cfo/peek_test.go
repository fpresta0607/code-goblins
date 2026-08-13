package main

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
)

func TestRunPeekStreamsOnlyTail(t *testing.T) {
	deps := testCommandRuntime(t)
	var gotTarget string
	var gotLines int
	deps.peek = func(_ context.Context, _ home.Home, target string, lines int) (string, error) {
		gotTarget, gotLines = target, lines
		return "marker\n", nil
	}

	var stdout, stderr bytes.Buffer
	exit := runWithRuntime([]string{"peek", "fm-g1", "25"}, &stdout, &stderr, deps)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", exit, stderr.String())
	}
	if gotTarget != "fm-g1" || gotLines != 25 {
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
	exit := runWithRuntime([]string{"peek", "fm-g1"}, &stdout, &stderr, deps)
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
	exit := runWithRuntime([]string{"peek", "fm-g1"}, &stdout, &stderr, deps)
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
