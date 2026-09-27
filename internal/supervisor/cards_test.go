package supervisor

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestSnapshotCarriesAGoblinsWholeStatusLineForShowMore(t *testing.T) {
	// Arrange
	handler, h := orderBoard(t)
	line := "working: " + strings.Repeat("checking every card in a real browser, ", 30)
	if err := state.AppendStatus(h.State, "task-1", line); err != nil {
		t.Fatal(err)
	}

	// Act
	snapshot, err := handler.Service.Snapshot()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range snapshot.Tasks {
		if task.ID == "task-1" && task.Activity != strings.TrimSpace(line) {
			t.Fatalf("the panel gets %d of the status line's %d characters, want all of it behind Show more", len(task.Activity), len(strings.TrimSpace(line)))
		}
	}
}

func TestSnapshotDatesEachSessionAndEachQueuedBrief(t *testing.T) {
	// Arrange
	handler, h := orderBoard(t)
	started := time.Date(2026, 9, 27, 9, 30, 0, 0, time.UTC)
	if err := state.WriteTaskMeta(h.State, state.TaskMeta{ID: "running", Project: h.Root, Worktree: h.Root, Harness: "claude", Mode: "no-mistakes", Kind: "ship", Backend: "native", SpawnGen: "s" + strconv.FormatInt(started.UnixNano(), 10)}); err != nil {
		t.Fatal(err)
	}
	before := time.Now().Add(-time.Minute)
	writeFile(t, filepath.Join(h.Data, "backlog.md"), "## Queued\n- **briefed-row** - Has a brief\n- **bare-row** - Has no brief\n")
	writeFile(t, filepath.Join(h.Data, "briefed-row", "brief.md"), "# Brief\n")
	writeFile(t, filepath.Join(h.Data, "brief-only", "brief.md"), "# Brief\n")

	// Act
	snapshot, err := handler.Service.Snapshot()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	since := map[string]time.Time{}
	for _, task := range snapshot.Tasks {
		since[task.ID] = task.Since
	}
	if !since["running"].Equal(started) {
		t.Errorf("a running goblin's clock starts at %v, want its spawn at %v", since["running"], started)
	}
	for _, id := range []string{"briefed-row", "brief-only"} {
		if since[id].Before(before) || since[id].After(time.Now()) {
			t.Errorf("%s waits since %v, want when its brief was written", id, since[id])
		}
	}
	if !since["bare-row"].IsZero() {
		t.Errorf("a row with no brief waits since %v, want no time at all", since["bare-row"])
	}
	if !since["task-1"].IsZero() {
		t.Errorf("a task whose generation carries no time started at %v, want none", since["task-1"])
	}
}
