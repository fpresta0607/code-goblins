package janitor

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/reap"
)

// The gate's reaper skips a repository folder that is a directory junction
// (no-mistakes#261), as a gate home moved to the Dev Drive by hand leaves
// them. The sweep counts the completed runs it keeps past its retention under
// such a folder apart from those under a folder of its own, so the wake names
// the cause.
func TestSweepCountsTheGatesCompletedRunsUnderAJunctionApart(t *testing.T) {
	// Arrange
	f := newSweepFixture(t)
	gate := filepath.Join(t.TempDir(), ".no-mistakes")
	newGateHome(t, gate, "", []gateRun{
		{repository: "repo1", id: "run-own", status: "completed", age: 48 * time.Hour},
		{repository: "moved", id: "run-linked-1", status: "completed", age: 30 * time.Hour},
		{repository: "moved", id: "run-linked-2", status: "completed", age: 96 * time.Hour},
		{repository: "moved", id: "run-linked-failed", status: "failed", age: 120 * time.Hour},
	})
	moved := filepath.Join(gate, "worktrees", "moved")
	target := filepath.Join(t.TempDir(), "devdrive", "no-mistakes", "worktrees", "moved")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(moved, target); err != nil {
		t.Fatal(err)
	}
	if out, err := execx.CommandContext(context.Background(), filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe"), "/d", "/c", "mklink", "/J", moved, target).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J: %v: %s", err, out)
	}
	cfg := f.config(reap.Inventory{})
	cfg.GateHomes = []string{gate}
	cfg.GateRoot = gate

	// Act
	record := Sweep(context.Background(), cfg)

	// Assert
	past := record.Gate.PastRetention
	if past == nil || past.Linked.Count != 2 || past.Unlinked.Count != 1 {
		t.Fatalf("past retention = %+v, want 2 under the junction and 1 under a folder of its own; notes %q", past, record.Notes)
	}
	if age := f.now.Sub(past.Linked.Oldest); age < 95*time.Hour || age > 97*time.Hour {
		t.Errorf("the oldest under the junction was written %s ago, want 96 hours", age)
	}
}
