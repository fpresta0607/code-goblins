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
