package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/lock"
)

func registerHome(t *testing.T) home.Home {
	t.Helper()
	dir := t.TempDir()
	h := home.Home{Root: dir, State: filepath.Join(dir, "state")}
	if err := os.MkdirAll(h.State, 0o755); err != nil {
		t.Fatal(err)
	}
	return h
}

func TestSessionStartRegistersTheSessionThatHoldsTheHome(t *testing.T) {
	h := registerHome(t)
	if _, err := lock.Acquire(h.State); err != nil {
		t.Fatal(err)
	}
	hostAttachTestTerminal(t, h.State, "cfo")
	standInAsTerminalProgram(t, h.State, "cfo")
	var stdout bytes.Buffer
	registerPrimary(h, os.Getpid(), "claude", "s1", &stdout)
	want := "CFO REGISTRATION: claude pid " + strconv.Itoa(os.Getpid()) + " in native terminal cfo\n"
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
	t.Setenv(host.IDVariable, "cfo")
	var stdout bytes.Buffer
	registerPrimary(h, os.Getpid(), "claude", "s1", &stdout)
	if stdout.Len() != 0 {
		t.Fatalf("read-only session: stdout %q; want silence", stdout.String())
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

// A session in no native terminal has nothing the board could reach: its
// SessionStart stays silent, even inside a Herdr pane, and cfo register says
// why it registered nothing.
func TestASessionOutsideANativeTerminalRegistersNothing(t *testing.T) {
	h := registerHome(t)
	t.Setenv(host.IDVariable, "")
	t.Setenv("HERDR_PANE_ID", "w1:p1")
	if _, err := lock.Acquire(h.State); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Release(h.State) })
	var hook, stdout, stderr bytes.Buffer

	registerPrimary(h, os.Getpid(), "claude", "s1", &hook)
	exit := runRegister(nil, &stdout, &stderr, commandRuntime{resolveHome: func() (home.Home, error) { return h, nil }})

	if hook.Len() != 0 {
		t.Errorf("SessionStart wrote %q, want silence", hook.String())
	}
	if exit != 1 || !strings.Contains(stderr.String(), "runs in no native terminal") {
		t.Errorf("cfo register: exit %d stderr %q, want 1 and the reason", exit, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(h.State, "primary.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("primary.json stat = %v, want no registration written", err)
	}
}

// A CFO's SessionStart in a native terminal registers that terminal. Here the
// terminal's host is gone, so the hook reports the refusal.
func TestSessionStartRegistersInANativeTerminal(t *testing.T) {
	h := registerHome(t)
	t.Setenv(host.IDVariable, "cfo")
	if _, err := lock.Acquire(h.State); err != nil {
		t.Fatal(err)
	}
	// A killed host's record: it names this process as the terminal's
	// program, and nothing serves its pipe.
	data, err := json.Marshal(host.Record{ID: "cfo", Pipe: `\\.\pipe\code-goblins-host-test-` + strconv.Itoa(os.Getpid()), Token: "token", Version: host.Version, HostPID: os.Getpid(), ChildPID: os.Getpid(), Started: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(h.State, "hosts"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.State, "hosts", "cfo.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer

	registerPrimary(h, os.Getpid(), "claude", "s1", &stdout)

	if !strings.Contains(stdout.String(), "CFO REGISTRATION FAILED") || !strings.Contains(stdout.String(), "does not answer") {
		t.Fatalf("stdout = %q, want the native terminal's registration refused because its host does not answer", stdout.String())
	}
}
