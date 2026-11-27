package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/wake"
)

// A failure beside a healthy serve is not a stall: when recording the rewake
// fails while serve holds the watcher with a fresh heartbeat, the banner names
// that error and does not send the CEO to restart a serve that is working.
func TestAutoarmNamesTheRealErrorBesideAHealthyServe(t *testing.T) {
	dir := newCEOHome(t)
	setTinyAutoarmIntervals(t)
	state := filepath.Join(dir, "state")
	writeMetaFixture(t, state, "g1.meta")
	servingWatcher(t, state)
	if _, err := wake.Append(state, "notify", "g1", "blocked: Should I merge this?"); err != nil {
		t.Fatal(err)
	}
	rewoken := filepath.Join(state, rewokenFile)
	if err := os.WriteFile(rewoken, []byte("0\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(rewoken, 0o644) })

	exit, stderr, _ := runAutoarm(t)

	if exit != 2 || !strings.Contains(stderr, "record the rewake") {
		t.Fatalf("exit=%d stderr=%q, want the failure banner naming the rewake error", exit, stderr)
	}
	if strings.Contains(stderr, "restart goblins serve") {
		t.Errorf("stderr %q tells the CEO to restart a serve whose heartbeat is fresh", stderr)
	}
}
