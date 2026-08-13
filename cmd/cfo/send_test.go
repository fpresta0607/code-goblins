package main

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
)

func TestRunSendStreamsConfirmedTextResult(t *testing.T) {
	deps := testCommandRuntime(t)
	var gotTarget, gotText string
	deps.sendText = func(_ context.Context, _ home.Home, target, text string) error {
		gotTarget, gotText = target, text
		return nil
	}

	var stdout, stderr bytes.Buffer
	exit := runWithRuntime([]string{"send", "fm-g1", "print", "the", "marker"}, &stdout, &stderr, deps)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", exit, stderr.String())
	}
	if gotTarget != "fm-g1" || gotText != "print the marker" {
		t.Errorf("send = target %q text %q, want parsed input", gotTarget, gotText)
	}
	if stdout.String() != "sent fm-g1\n" || stderr.Len() != 0 {
		t.Errorf("stdout=%q stderr=%q, want only user-facing confirmation", stdout.String(), stderr.String())
	}
}

func TestRunSendRoutesKeyWithoutText(t *testing.T) {
	deps := testCommandRuntime(t)
	var gotTarget, gotKey string
	deps.sendKey = func(_ context.Context, _ home.Home, target, key string) error {
		gotTarget, gotKey = target, key
		return nil
	}
	deps.sendText = func(context.Context, home.Home, string, string) error {
		t.Fatal("key request called text sender")
		return nil
	}

	var stdout, stderr bytes.Buffer
	exit := runWithRuntime([]string{"send", "fleet:pane-1", "--key", "Ctrl-C"}, &stdout, &stderr, deps)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", exit, stderr.String())
	}
	if gotTarget != "fleet:pane-1" || gotKey != "Ctrl-C" {
		t.Errorf("key request = target %q key %q, want parsed input", gotTarget, gotKey)
	}
	if stdout.String() != "sent key Ctrl-C to fleet:pane-1\n" || stderr.Len() != 0 {
		t.Errorf("stdout=%q stderr=%q, want only key confirmation", stdout.String(), stderr.String())
	}
}

func TestRunSendRejectsMixedKeyAndTextWithoutState(t *testing.T) {
	h := testHome(t)
	deps := testCommandRuntimeForHome(h)
	called := false
	deps.sendKey = func(context.Context, home.Home, string, string) error { called = true; return nil }
	deps.sendText = func(context.Context, home.Home, string, string) error { called = true; return nil }

	var stdout, stderr bytes.Buffer
	exit := runWithRuntime([]string{"send", "fm-g1", "--key", "Enter", "extra"}, &stdout, &stderr, deps)
	if exit != 2 {
		t.Fatalf("exit = %d, want 2", exit)
	}
	if called {
		t.Fatal("invalid send invoked a service")
	}
	if stdout.Len() != 0 || stderr.Len() == 0 {
		t.Errorf("stdout=%q stderr=%q, want diagnostics only", stdout.String(), stderr.String())
	}
}

func TestRunSendRejectsUnknownFlagInTargetPosition(t *testing.T) {
	deps := testCommandRuntime(t)
	called := false
	deps.sendText = func(context.Context, home.Home, string, string) error { called = true; return nil }

	var stdout, stderr bytes.Buffer
	exit := runWithRuntime([]string{"send", "--unknown", "message"}, &stdout, &stderr, deps)
	if exit != 2 {
		t.Fatalf("exit = %d, want 2; stderr=%s", exit, stderr.String())
	}
	if called {
		t.Fatal("unknown send flag invoked text sender")
	}
}

func TestRunSendWritesFailureOnlyToStderr(t *testing.T) {
	deps := testCommandRuntime(t)
	deps.sendText = func(context.Context, home.Home, string, string) error { return errors.New("delivery unconfirmed") }

	var stdout, stderr bytes.Buffer
	exit := runWithRuntime([]string{"send", "fm-g1", "draft"}, &stdout, &stderr, deps)
	if exit != 1 {
		t.Fatalf("exit = %d, want 1", exit)
	}
	if stdout.Len() != 0 || stderr.String() != "delivery unconfirmed\n" {
		t.Errorf("stdout=%q stderr=%q, want diagnostics only", stdout.String(), stderr.String())
	}
}
