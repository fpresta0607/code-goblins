package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/spawn"
)

func TestRunSpawnPassesValidatedRequestAndEnvironment(t *testing.T) {
	homeRoot := t.TempDir()
	t.Setenv("CFO_HOME", homeRoot)
	t.Setenv("HERDR_SESSION", "fleet-session")

	deps := defaultCommandRuntime()
	var gotHome home.Home
	var gotRequest spawn.Request
	deps.spawn = func(_ context.Context, h home.Home, request spawn.Request) (spawn.Result, error) {
		gotHome = h
		gotRequest = request
		return spawn.Result{Output: "spawned g1"}, nil
	}

	var stdout, stderr bytes.Buffer
	exit := runWithRuntime([]string{
		"spawn", "g1",
		"--project", `C:\project`,
		"--brief", `C:\brief.md`,
		"--harness", "codex",
		"--mode", "direct-PR",
		"--model", "gpt-5",
		"--effort", "high",
		"--yolo",
	}, &stdout, &stderr, deps)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", exit, stderr.String())
	}
	if gotHome.Root != homeRoot || gotHome.State != filepath.Join(homeRoot, "state") {
		t.Errorf("home = %+v, want CFO_HOME %q", gotHome, homeRoot)
	}
	if gotRequest.ID != "g1" || gotRequest.Project != `C:\project` || gotRequest.BriefPath != `C:\brief.md` || gotRequest.Kind != "ship" || gotRequest.Mode != "direct-PR" || !gotRequest.Yolo || string(gotRequest.Harness) != "codex" || gotRequest.Model != "gpt-5" || gotRequest.Effort != "high" || gotRequest.Session != "fleet-session" {
		t.Errorf("request = %+v, want parsed spawn request", gotRequest)
	}
	if stdout.String() != "spawned g1\n" || stderr.Len() != 0 {
		t.Errorf("stdout=%q stderr=%q, want only result output", stdout.String(), stderr.String())
	}
}

func TestRunSpawnDefaultsSessionAndDeliveryMode(t *testing.T) {
	deps := testCommandRuntime(t)
	var got spawn.Request
	deps.spawn = func(_ context.Context, _ home.Home, request spawn.Request) (spawn.Result, error) {
		got = request
		return spawn.Result{Output: "spawned g2"}, nil
	}

	var stdout, stderr bytes.Buffer
	exit := runWithRuntime([]string{"spawn", "g2", "--project", `C:\project`, "--brief", `C:\brief.md`, "--harness", "claude"}, &stdout, &stderr, deps)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", exit, stderr.String())
	}
	if got.Session != "default" || got.Mode != "no-mistakes" || got.Kind != "ship" {
		t.Errorf("request = %+v, want default session, ship kind, and no-mistakes mode", got)
	}
}

func TestRunSpawnRejectsInvalidArgumentsWithoutState(t *testing.T) {
	h := testHome(t)
	deps := testCommandRuntimeForHome(h)
	called := false
	deps.spawn = func(context.Context, home.Home, spawn.Request) (spawn.Result, error) {
		called = true
		return spawn.Result{}, nil
	}

	var stdout, stderr bytes.Buffer
	exit := runWithRuntime([]string{"spawn", "g3", "--project", `C:\project`, "--brief", `C:\brief.md`, "--harness", "grok"}, &stdout, &stderr, deps)
	if exit != 2 {
		t.Fatalf("exit = %d, want 2; stderr=%s", exit, stderr.String())
	}
	if called {
		t.Fatal("invalid harness invoked spawn service")
	}
	if stdout.Len() != 0 || stderr.Len() == 0 {
		t.Errorf("stdout=%q stderr=%q, want diagnostics only", stdout.String(), stderr.String())
	}
	if _, err := os.Stat(h.State); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("invalid spawn created state: stat error = %v", err)
	}
}

func TestRunSpawnRejectsUnknownFlagInTaskPosition(t *testing.T) {
	deps := testCommandRuntime(t)
	called := false
	deps.spawn = func(context.Context, home.Home, spawn.Request) (spawn.Result, error) {
		called = true
		return spawn.Result{}, nil
	}

	var stdout, stderr bytes.Buffer
	exit := runWithRuntime([]string{"spawn", "--unknown", "--project", `C:\project`, "--brief", `C:\brief.md`, "--harness", "claude"}, &stdout, &stderr, deps)
	if exit != 2 {
		t.Fatalf("exit = %d, want 2; stderr=%s", exit, stderr.String())
	}
	if called {
		t.Fatal("unknown spawn flag invoked spawn service")
	}
}

func TestRunSpawnWritesFailureOnlyToStderr(t *testing.T) {
	deps := testCommandRuntime(t)
	deps.spawn = func(context.Context, home.Home, spawn.Request) (spawn.Result, error) {
		return spawn.Result{}, errors.New("spawn failed")
	}

	var stdout, stderr bytes.Buffer
	exit := runWithRuntime([]string{"spawn", "g4", "--project", `C:\project`, "--brief", `C:\brief.md`, "--harness", "pi"}, &stdout, &stderr, deps)
	if exit != 1 {
		t.Fatalf("exit = %d, want 1", exit)
	}
	if stdout.Len() != 0 || stderr.String() != "spawn failed\n" {
		t.Errorf("stdout=%q stderr=%q, want diagnostics only", stdout.String(), stderr.String())
	}
}

func testHome(t *testing.T) home.Home {
	t.Helper()
	root := t.TempDir()
	return home.Home{Root: root, State: filepath.Join(root, "state"), Data: filepath.Join(root, "data")}
}

func testCommandRuntime(t *testing.T) commandRuntime {
	t.Helper()
	return testCommandRuntimeForHome(testHome(t))
}

func testCommandRuntimeForHome(h home.Home) commandRuntime {
	return commandRuntime{
		resolveHome: func() (home.Home, error) { return h, nil },
	}
}
