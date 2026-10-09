package fleet

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func completedHome(t *testing.T, backlog string) home.Home {
	t.Helper()
	root := t.TempDir()
	h := home.Home{Root: root, State: filepath.Join(root, "state"), Data: filepath.Join(root, "data")}
	for _, dir := range []string{h.State, h.Data} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(h.Data, "backlog.md"), []byte(backlog), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteOutcome(h.State, state.Outcome{ID: "g1", Phase: "done", Evidence: "reported pull request", At: time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
	return h
}

// cfo backlog done closes the row of a delivered task alone, while a
// retirement closes the row of any retired task, as retired when it did not
// deliver.
func TestOnlyARetirementClosesAnUndeliveredTasksRow(t *testing.T) {
	// Arrange
	const backlog = "## Queued\n- **g1** - Ship it\n\n## Done\n"
	h := completedHome(t, backlog)
	if err := state.WriteOutcome(h.State, state.Outcome{ID: "g1", Phase: "stopped", Reason: "Worktree returned by cleanup", At: time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}

	// Act
	completeErr := CompleteQueuedTask(h, "g1")
	left, err := os.ReadFile(filepath.Join(h.Data, "backlog.md"))
	if err != nil {
		t.Fatal(err)
	}
	retireErr := RetireQueuedTask(h, "g1")

	// Assert
	if completeErr == nil || string(left) != backlog {
		t.Errorf("CompleteQueuedTask = %v, backlog =\n%s\nwant an undelivered task's row refused and left as it was", completeErr, left)
	}
	got, err := os.ReadFile(filepath.Join(h.Data, "backlog.md"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "## Queued\n\n## Done\n- [x] g1 - Ship it (retired 2026-10-07)\n"; retireErr != nil || string(got) != want {
		t.Errorf("RetireQueuedTask = %v, backlog =\n%s\nwant\n%s", retireErr, got, want)
	}
}

// A task with no row anywhere in the backlog is not queued, which a cleanup
// of a task spawned straight from a brief meets every time.
func TestCompleteQueuedTaskWithNoRowIsNotQueued(t *testing.T) {
	// Arrange
	h := completedHome(t, "## Queued\n- **other** - Other\n")

	// Act
	err := CompleteQueuedTask(h, "g1")

	// Assert
	if !errors.Is(err, ErrNotQueued) {
		t.Fatalf("err = %v, want ErrNotQueued", err)
	}
}
