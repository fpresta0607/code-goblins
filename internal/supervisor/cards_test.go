package supervisor

import (
	"os"
	"path/filepath"
	"runtime"
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
	testStarted := time.Now()
	started := time.Date(2026, 9, 27, 9, 30, 0, 0, time.UTC)
	switched := filepath.Join(h.Root, "switched-worktree")
	if err := os.MkdirAll(switched, 0o700); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(h.Root, "missing-worktree")
	for _, meta := range []state.TaskMeta{
		{ID: "running", Worktree: missing, SpawnGen: "s" + strconv.FormatInt(started.UnixNano(), 10)},
		{ID: "switched", Worktree: switched, SpawnGen: "s" + strconv.FormatInt(testStarted.Add(time.Hour).UnixNano(), 10)},
		{ID: "timeless", Worktree: missing, SpawnGen: "g1"},
	} {
		meta.Project, meta.Harness, meta.Mode, meta.Kind, meta.Backend = h.Root, "claude", "no-mistakes", "ship", "native"
		if err := state.WriteTaskMeta(h.State, meta); err != nil {
			t.Fatal(err)
		}
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
		t.Errorf("a running goblin with no worktree folder starts at %v, want its spawn at %v", since["running"], started)
	}
	if runtime.GOOS == "windows" {
		if since["switched"].Before(testStarted.Add(-5*time.Second)) || since["switched"].After(time.Now().Add(5*time.Second)) {
			t.Errorf("a switched goblin's clock starts at %v, want its worktree's creation near %v, not its new spawn", since["switched"], testStarted)
		}
		for _, id := range []string{"briefed-row", "brief-only"} {
			if since[id].Before(before) || since[id].After(time.Now()) {
				t.Errorf("%s waits since %v, want when its brief was written", id, since[id])
			}
		}
	}
	if !since["bare-row"].IsZero() {
		t.Errorf("a row with no brief waits since %v, want no time at all", since["bare-row"])
	}
	if !since["timeless"].IsZero() {
		t.Errorf("a task with no worktree folder whose generation carries no time started at %v, want none", since["timeless"])
	}
}
