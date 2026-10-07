package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// queueWork queues one task, with its brief, in the home at dir.
func queueWork(t *testing.T, dir string) {
	t.Helper()
	for path, content := range map[string]string{
		filepath.Join(dir, "data", "backlog.md"):            "## Queued\n- **next-task** - Ship it (repo: code-goblins)\n",
		filepath.Join(dir, "data", "next-task", "brief.md"): "# Brief next-task\n\n## Project\n\nC:\\dev\\code-goblins\n\n## Task\n\nShip it.\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// freeMemory makes the hooks read available GB of memory and of commit.
func freeMemory(t *testing.T, available float64) {
	t.Helper()
	prior := hookMemory
	hookMemory = func() (supervisor.Memory, error) {
		bytes := uint64(available * (1 << 30))
		return supervisor.Memory{Available: bytes, CommitAvailable: bytes}, nil
	}
	t.Cleanup(func() { hookMemory = prior })
}

// holdHome makes session the one holding the home, as the CFO's own
// SessionStart leaves it.
func holdHome(t *testing.T, state, session string) {
	t.Helper()
	if _, err := lock.AcquireOwner(state, os.Getpid(), session); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Release(state) })
}

// A CFO turn that ends with no goblin in flight while queued work waits and
// memory is free is reopened at once, with the next work named and the wake
// on the queue for cfo drain; the same next work does not reopen the next
// turn end. On 2026-10-07 the CFO retired its last goblin at 08:20Z with 30
// rows queued and 13.6 GB free, the Stop hooks saw nothing in flight, and
// the CFO idled four hours.
func TestTurnendGuardReopensAnIdleTurnWhileWorkWaits(t *testing.T) {
	cases := []struct {
		name       string
		session    string
		available  float64
		wantReopen bool
	}{
		{name: "the CFO holding the home, memory free", session: "s1", available: 8, wantReopen: true},
		{name: "a session that does not hold the home", session: "other", available: 8},
		{name: "memory short", session: "s1", available: 4.5},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			dir := newPrimaryHome(t)
			state := filepath.Join(dir, "state")
			queueWork(t, dir)
			holdHome(t, state, "s1")
			freeMemory(t, testCase.available)
			payload := `{"session_id":"` + testCase.session + `"}`

			// Act
			var stdout, stderr bytes.Buffer
			exit := runHook("turnend-guard", strings.NewReader(payload), &stdout, &stderr)
			var again bytes.Buffer
			second := runHook("turnend-guard", strings.NewReader(payload), &stdout, &again)

			// Assert
			pending, err := wake.Pending(state)
			if err != nil {
				t.Fatal(err)
			}
			if !testCase.wantReopen {
				if exit != 0 || len(pending) != 0 {
					t.Fatalf("exit=%d stderr=%q wakes=%v, want the turn left to end", exit, stderr.String(), pending)
				}
				return
			}
			if exit != 2 || !strings.Contains(stderr.String(), "next: start next-task") || !strings.Contains(stderr.String(), "cfo drain") {
				t.Fatalf("exit=%d stderr=%q, want the turn reopened naming next-task", exit, stderr.String())
			}
			if len(pending) != 1 || pending[0].Kind != "idle" {
				t.Fatalf("wakes=%v, want the idle wake on the queue", pending)
			}
			if rewoken, err := readRewoken(state); err != nil || rewoken < pending[0].Seq {
				t.Errorf("rewoken=%d %v, want the reopened turn to cover seq %d so the auto-arm does not rewake for it", rewoken, err, pending[0].Seq)
			}
			if second != 0 {
				t.Errorf("second turn end exit=%d stderr=%q, want the same next work to let it end", second, again.String())
			}
		})
	}
}

// The auto-arm keeps watching while queued work waits with no goblin in
// flight, so the wakes that work raises reopen the CFO's turn: the memory
// coming back, the scheduler's idle wake, a start that failed.
func TestAutoarmStaysArmedWhileQueuedWorkWaits(t *testing.T) {
	// Arrange
	dir := newPrimaryHome(t)
	setAncestorPID(t, os.Getpid())
	setTinyAutoarmIntervals(t)
	t.Setenv("CFO_CLAUDE_AUTOARM_WAIT", "30")
	state := filepath.Join(dir, "state")
	queueWork(t, dir)
	servingWatcher(t, state)
	done := make(chan struct{})
	go func() {
		defer close(done)
		time.Sleep(1500 * time.Millisecond)
		if _, err := wake.Append(state, "idle", "fleet", "idle: nothing has started for 30 minutes"); err != nil {
			t.Error(err)
		}
	}()
	defer func() { <-done }()

	// Act
	exit, stderr, _ := runAutoarm(t)

	// Assert
	if exit != 2 || !strings.Contains(stderr, "idle:fleet") {
		t.Fatalf("exit=%d stderr=%q, want the rewake banner naming idle:fleet", exit, stderr)
	}
	assertEpochOutcome(t, state, "rewake")
}
