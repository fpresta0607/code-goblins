package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
)

// paneRunner is a Herdr in which this test process is the foreground harness
// of pane w1:p1 and no agent has been detected yet.
type paneRunner struct{ calls int }

func (r *paneRunner) Run(_ context.Context, req execx.Request) (execx.Result, error) {
	r.calls++
	a := req.Args
	switch {
	case len(a) >= 2 && a[0] == "pane" && a[1] == "process-info":
		return execx.Result{Stdout: []byte(fmt.Sprintf(`{"result":{"process_info":{"shell_pid":1,"foreground_process_group_id":%d}}}`, os.Getpid()))}, nil
	case len(a) >= 2 && a[0] == "api" && a[1] == "snapshot":
		return execx.Result{Stdout: []byte(`{"result":{"type":"session_snapshot","snapshot":{"protocol":1,"panes":[{"pane_id":"w1:p1","tab_id":"w1:t1","workspace_id":"w1","terminal_id":"t-1"}],"agents":[]}}}`)}, nil
	}
	return execx.Result{}, fmt.Errorf("unexpected Herdr operation: %v", a)
}

func registerHome(t *testing.T) home.Home {
	t.Helper()
	dir := t.TempDir()
	h := home.Home{Root: dir, State: filepath.Join(dir, "state")}
	if err := os.MkdirAll(h.State, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_PANE_ID", "w1:p1")
	return h
}

func TestSessionStartRegistersTheSessionThatHoldsTheHome(t *testing.T) {
	h := registerHome(t)
	if _, err := lock.Acquire(h.State); err != nil {
		t.Fatal(err)
	}
	runner := &paneRunner{}
	var stdout bytes.Buffer
	registerPrimary(h, os.Getpid(), "claude", "s1", &herdr.Client{Commands: runner, Session: "isolated"}, &stdout)
	want := "CFO REGISTRATION: claude pid " + strconv.Itoa(os.Getpid()) + " in Herdr pane isolated:w1:p1\n"
	if stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
	data, err := os.ReadFile(filepath.Join(h.State, "primary.json"))
	if err != nil || !strings.Contains(string(data), `"agent":"claude"`) || !strings.Contains(string(data), `"session":"s1"`) {
		t.Fatalf("primary.json = %s, %v", data, err)
	}
}

// A session that opened read-only, because another live CFO holds the home,
// must never take over the board's delivery target.
func TestSessionStartNeverRegistersAReadOnlySession(t *testing.T) {
	h := registerHome(t)
	other := exec.Command("cmd", "/c", "ping -n 30 127.0.0.1 >NUL")
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Process.Kill(); _, _ = other.Process.Wait() })
	if _, err := lock.AcquireOwner(h.State, other.Process.Pid, "other"); err != nil {
		t.Fatal(err)
	}
	runner := &paneRunner{}
	var stdout bytes.Buffer
	registerPrimary(h, os.Getpid(), "claude", "s1", &herdr.Client{Commands: runner}, &stdout)
	if stdout.Len() != 0 || runner.calls != 0 {
		t.Fatalf("read-only session: stdout %q, %d Herdr calls; want silence", stdout.String(), runner.calls)
	}
	if _, err := os.Stat(filepath.Join(h.State, "primary.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only session wrote primary.json: %v", err)
	}
}

func TestRegisterCommandRefusesAGoblin(t *testing.T) {
	t.Setenv(harness.RoleVariable, harness.RoleGoblin)
	runtime := commandRuntime{resolveHome: func() (home.Home, error) {
		t.Fatal("a refused registration resolved the home")
		return home.Home{}, nil
	}}
	var stdout, stderr bytes.Buffer
	if exit := runRegister(nil, &stdout, &stderr, runtime); exit != 1 || !strings.Contains(stderr.String(), "a goblin is never the primary CFO") {
		t.Fatalf("exit %d stderr %q, want 1 and the goblin refusal", exit, stderr.String())
	}
}
