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

// Closing a row moves only its own block: a row indented under it is another
// task's row, which the board reads as queued work, and it stays queued.
func TestCompleteQueuedTaskLeavesAnIndentedRowOfAnotherTaskQueued(t *testing.T) {
	// Arrange
	h := completedHome(t, "## Queued\n- **g1** - Ship it\n  detail: mine\n  - **child** - Another task\n    detail: its own\n\n## Done\n")

	// Act
	err := CompleteQueuedTask(h, "g1")

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(h.Data, "backlog.md"))
	if want := "## Queued\n  - **child** - Another task\n    detail: its own\n\n## Done\n- [x] g1 - Ship it (done 2026-10-07)\n  detail: mine\n"; string(got) != want {
		t.Errorf("backlog =\n%s\nwant\n%s", got, want)
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
