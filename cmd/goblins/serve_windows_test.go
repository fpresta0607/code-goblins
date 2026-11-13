package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

func TestTheFirstRunStartWaitsForAnotherCEOStartAndKeepsItsTerminal(t *testing.T) {
	h := testHome(t)
	if err := os.MkdirAll(h.State, 0o700); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	copyFile(t, self, filepath.Join(bin, "claude.exe"))
	t.Setenv("PATH", bin)
	run := firstRunOn(h, t.TempDir(), true, func(string) error { return nil })
	if _, err := lock.AcquireExclusiveNamed(h.State, supervisor.CEOLaunchLock); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lock.ReleaseExclusiveNamed(h.State, supervisor.CEOLaunchLock) })
	finished := make(chan error, 1)

	go func() { finished <- run.StartCEO("claude") }()
	select {
	case err := <-finished:
		if err == nil {
			closeNativeTerminal(t, h.State, supervisor.NativeCEOTerminal)
		}
		t.Fatalf("first-run Start returned while another start held the lock: %v", err)
	// stall-accepted: nothing is meant to finish while the lock is held, and the start's own wait for it is 30 s, so a stall only makes this wait longer.
	case <-time.After(200 * time.Millisecond):
	}
	if err := startNativeCEO(h, h.Root, "claude", nil, ""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeNativeTerminal(t, h.State, supervisor.NativeCEOTerminal) })
	before, err := host.ReadRecord(h.State, supervisor.NativeCEOTerminal)
	if err != nil {
		t.Fatal(err)
	}
	if err := lock.ReleaseExclusiveNamed(h.State, supervisor.CEOLaunchLock); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Minute):
		t.Fatal("first-run Start did not finish after the launch lock was released")
	}

	after, err := host.ReadRecord(h.State, supervisor.NativeCEOTerminal)
	if err != nil || before.HostPID != after.HostPID || before.Token != after.Token {
		t.Fatalf("first-run Start replaced the running terminal: before %+v, after %+v, error %v", before, after, err)
	}
}
