package janitor

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/fpresta0607/code-goblins/internal/reap"
)

// gateRun is one run a test's gate home records, its checkout's folder and
// how long ago the run finished and its checkout was last written.
type gateRun struct {
	repository, id, status string
	age                    time.Duration
}

// newGateHome makes a gate home at root with a database recording runs, each
// last updated as its age says, a checkout under worktrees for each, and
// config as its config.yaml when it is not empty.
func newGateHome(t *testing.T, root, config string, runs []gateRun) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if config != "" {
		writeFile(t, filepath.Join(root, "config.yaml"), config)
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE runs (id TEXT PRIMARY KEY, repo_id TEXT, status TEXT, updated_at INTEGER)"); err != nil {
		t.Fatal(err)
	}
	for _, run := range runs {
		if _, err := db.Exec("INSERT INTO runs (id, repo_id, status, updated_at) VALUES (?, ?, ?, ?)", run.id, run.repository, run.status, time.Now().Add(-run.age).Unix()); err != nil {
			t.Fatal(err)
		}
		checkout := filepath.Join(root, "worktrees", run.repository, run.id)
		writeFile(t, filepath.Join(checkout, "go.mod"), "module x\n")
		makeAged(t, checkout, run.age)
	}
}

// The gate removes a completed run's leftover checkout once it is older than
// its worktree.retention, 24 hours unless its config.yaml says otherwise, and
// keeps every one when that says unlimited. The sweep counts the checkouts
// the gate's home keeps past that rule: only those of runs its database
// records as completed, since a running run, a parked one, one whose CI watch
// was cut short and a failed or cancelled one it could still continue are the
// gate's to keep.
func TestSweepCountsTheGatesCompletedRunsKeptPastItsRetention(t *testing.T) {
	runs := []gateRun{
		{repository: "repo1", id: "run-completed", status: "completed", age: 72 * time.Hour},
		{repository: "repo1", id: "run-old", status: "completed", age: 48 * time.Hour},
		{repository: "repo2", id: "run-newer", status: "completed", age: 30 * time.Hour},
		{repository: "repo1", id: "run-failed", status: "failed", age: 72 * time.Hour},
		{repository: "repo2", id: "run-cancelled", status: "cancelled", age: 72 * time.Hour},
		{repository: "repo1", id: "run-recent", status: "completed", age: time.Hour},
		{repository: "repo1", id: "run-parked", status: "running", age: 72 * time.Hour},
		{repository: "repo2", id: "run-interrupted", status: "ci_monitor_interrupted", age: 72 * time.Hour},
	}
	for _, testCase := range []struct {
		name      string
		config    string
		wantCount int
		wantNone  bool
	}{
		{name: "no config", wantCount: 3},
		{name: "a config without worktree settings", config: "agent: claude\n", wantCount: 3},
		{name: "a two-day retention", config: "worktree:\n  retention: 36h\n", wantCount: 2},
		{name: "an unlimited retention", config: "worktree:\n  retention: unlimited\n", wantNone: true},
		{name: "a retention of 0", config: "worktree:\n  retention: 0s\n", wantNone: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			f := newSweepFixture(t)
			gate := filepath.Join(t.TempDir(), ".no-mistakes")
			newGateHome(t, gate, testCase.config, runs)
			cfg := f.config(reap.Inventory{})
			cfg.GateHomes = []string{gate}
			cfg.GateRoot = gate

			// Act
			record := Sweep(context.Background(), cfg)

			// Assert
			if record.Gate == nil {
				t.Fatal("the gate's home was not measured")
			}
			past := record.Gate.PastRetention
			if testCase.wantNone {
				if past != nil {
					t.Fatalf("past retention = %+v for a gate that keeps every checkout, want none", past)
				}
				return
			}
			if past == nil || past.Unlinked.Count != testCase.wantCount || past.Linked.Count != 0 {
				t.Fatalf("past retention = %+v, want %d completed runs' checkouts under folders of their own; notes %q", past, testCase.wantCount, record.Notes)
			}
			if age := f.now.Sub(past.Unlinked.Oldest); age < 71*time.Hour || age > 73*time.Hour {
				t.Errorf("oldest was written %s ago, want the completed run's 72 hours", age)
			}
		})
	}
}

// The gate's reaper runs as a run finishes, never on a timer, so after a quiet
// night a checkout whose retention ended since the last run finished is one
// the reaper has not had a turn at, not one it kept; once any run, failed
// too, finishes after that retention ended, the checkout is past its rule.
func TestSweepCountsOnlyTheCheckoutsTheGatesReaperHadATurnAt(t *testing.T) {
	left := gateRun{repository: "repo1", id: "run-left", status: "completed", age: 30 * time.Hour}
	for _, testCase := range []struct {
		name      string
		runs      []gateRun
		wantCount int
	}{
		{name: "a quiet night since the run finished", runs: []gateRun{left}},
		{name: "a quiet night since a run finished before its retention ended", runs: []gateRun{left, {repository: "repo2", id: "run-earlier", status: "completed", age: 8 * time.Hour}}},
		{name: "a failed run finished since its retention ended", runs: []gateRun{left, {repository: "repo2", id: "run-since", status: "failed", age: 2 * time.Hour}}, wantCount: 1},
		{name: "a running run since its retention ended", runs: []gateRun{left, {repository: "repo2", id: "run-running", status: "running", age: 2 * time.Hour}}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange: a completed run's checkout written 30 hours ago, past the
			// 24-hour retention six hours ago.
			f := newSweepFixture(t)
			gate := filepath.Join(t.TempDir(), ".no-mistakes")
			newGateHome(t, gate, "", testCase.runs)
			cfg := f.config(reap.Inventory{})
			cfg.GateHomes = []string{gate}
			cfg.GateRoot = gate

			// Act
			record := Sweep(context.Background(), cfg)

			// Assert
			if record.Gate == nil || record.Gate.PastRetention == nil {
				t.Fatalf("gate = %+v, want its runs read; notes %q", record.Gate, record.Notes)
			}
			if count := record.Gate.PastRetention.Unlinked.Count; count != testCase.wantCount {
				t.Fatalf("past retention = %d, want %d", count, testCase.wantCount)
			}
		})
	}
}

// A repository folder that is a symbolic link, as a gate home moved by hand
// on Linux or macOS leaves it, is one the gate's reaper does not reach either
// (no-mistakes#261), so the completed runs it keeps past its retention there
// are counted apart from those under a folder of its own.
func TestSweepCountsTheGatesCompletedRunsUnderASymbolicLinkApart(t *testing.T) {
	// Arrange
	f := newSweepFixture(t)
	gate := filepath.Join(t.TempDir(), ".no-mistakes")
	newGateHome(t, gate, "", []gateRun{
		{repository: "repo1", id: "run-own", status: "completed", age: 48 * time.Hour},
		{repository: "moved", id: "run-linked", status: "completed", age: 30 * time.Hour},
		{repository: "repo1", id: "run-just-finished", status: "completed", age: time.Hour},
	})
	moved := filepath.Join(gate, "worktrees", "moved")
	target := filepath.Join(t.TempDir(), "moved")
	if err := os.Rename(moved, target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, moved); err != nil {
		t.Skipf("this account cannot make a symbolic link: %v", err)
	}
	cfg := f.config(reap.Inventory{})
	cfg.GateHomes = []string{gate}
	cfg.GateRoot = gate

	// Act
	record := Sweep(context.Background(), cfg)

	// Assert
	if record.Gate == nil {
		t.Fatal("the gate's home was not measured")
	}
	if past := record.Gate.PastRetention; past == nil || past.Linked.Count != 1 || past.Unlinked.Count != 1 {
		t.Fatalf("past retention = %+v, want 1 under the link and 1 under a folder of its own; notes %q", past, record.Notes)
	}
}

// A gate home whose database cannot be read is no evidence that it keeps
// nothing past its rule: the sweep says so and records nothing.
func TestSweepSaysWhenTheGatesRunsCannotBeRead(t *testing.T) {
	// Arrange
	f := newSweepFixture(t)
	gate := filepath.Join(t.TempDir(), ".no-mistakes")
	writeFile(t, filepath.Join(gate, "worktrees", "repo1", "run-1", "go.mod"), "module x\n")
	cfg := f.config(reap.Inventory{})
	cfg.GateHomes = []string{gate}
	cfg.GateRoot = gate

	// Act
	record := Sweep(context.Background(), cfg)

	// Assert
	if record.Gate == nil || record.Gate.PastRetention != nil {
		t.Fatalf("gate = %+v, want it measured with nothing read past its retention", record.Gate)
	}
	if notes := strings.Join(record.Notes, "\n"); !strings.Contains(notes, "the gate's finished runs kept past its retention could not be read") {
		t.Errorf("notes = %q, want the unread database said", notes)
	}
}
