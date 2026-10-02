package supervisor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// liveSizedFleet writes a home the size of the live one on 2026-10-02: a
// dozen goblins at work, each with its status log, seventeen queued tasks in
// the backlog, sixty briefs not dispatched yet, a hundred task folders, five
// hundred archived records and finished tasks with their handoffs.
func liveSizedFleet(t *testing.T) (*Service, home.Home) {
	t.Helper()
	store, h := testStore(t)
	write := func(path, text string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	work := filepath.Join(h.Root, "work")
	for i := range 12 {
		id := fmt.Sprintf("goblin-%02d", i)
		if err := state.WriteTaskMeta(h.State, state.TaskMeta{ID: id, Title: "Goblin " + id, Project: work, Worktree: work, Harness: "claude", Mode: "direct-PR", Kind: "ship", Backend: "native", SpawnGen: "g1"}); err != nil {
			t.Fatal(err)
		}
		var log strings.Builder
		for line := range 300 {
			fmt.Fprintf(&log, "%s working: step %d of the task\n", time.Now().UTC().Add(time.Duration(line-300)*time.Minute).Format(time.RFC3339), line)
		}
		write(filepath.Join(h.State, id+".status"), log.String())
		write(filepath.Join(h.Data, id, "brief.md"), "# Brief "+id+"\n\n## Project\n\n"+work+"\n\n## Task\n\nDo the work.\n")
	}
	var backlog strings.Builder
	backlog.WriteString("# Backlog\n\n## Queued\n")
	for i := range 17 {
		fmt.Fprintf(&backlog, "- [ ] queued-%02d - Queued task %d (repo: example)\n  Its detail line.\n", i, i)
	}
	backlog.WriteString("\n## Parked\n\n## Done\n")
	write(filepath.Join(h.Data, "backlog.md"), backlog.String())
	for i := range 60 {
		write(filepath.Join(h.Data, fmt.Sprintf("brief-%02d", i), "brief.md"), "# Brief\n\n## Project\n\n"+work+"\n\n## Task\n\nA task not dispatched yet.\n\nWith its detail.\n")
	}
	for i := range 28 {
		write(filepath.Join(h.Data, fmt.Sprintf("notes-%02d", i), "notes.md"), "notes\n")
	}
	for i := range 499 {
		write(filepath.Join(h.State, state.ArchiveDirName, fmt.Sprintf("done-%03d.20260901T000000Z.meta", i)), "title=Done\n")
	}
	for i := range 20 {
		write(filepath.Join(h.Data, "archive", "finished", fmt.Sprintf("done-%03d", i), "handoff.md"), "# Handoff\n")
	}
	return &Service{Store: store}, h
}

// comparable is a snapshot without the time it was taken at.
func comparable(t *testing.T, snapshot Snapshot) string {
	t.Helper()
	snapshot.At = time.Time{}
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// On 2026-10-02 the live board took 3 to 13 seconds to build a snapshot. The
// supervisor was not computing: it opened several hundred files for each
// one, and opening a file cost that machine 20 to 80 ms in bursts, while
// asking for a file's size and time cost almost nothing. So a snapshot of a
// fleet in which nothing changed opens no fleet file again, and says exactly
// what the one before it said.
func TestASnapshotOfAnUnchangedFleetOpensNoFleetFileAgain(t *testing.T) {
	// Arrange
	s, _ := liveSizedFleet(t)
	first, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	before := fsx.Opens()

	// Act
	second, err := s.Snapshot()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	const budget = 6
	opened := fsx.Opens() - before
	t.Logf("a snapshot of an unchanged fleet opened %d files", opened)
	if opened > budget {
		t.Errorf("a snapshot of an unchanged fleet opened %d files, want at most %d", opened, budget)
	}
	if len(first.Tasks) < 80 {
		t.Fatalf("the fleet shows %d tasks, want the live size", len(first.Tasks))
	}
	if comparable(t, first) != comparable(t, second) {
		t.Error("the second snapshot of an unchanged fleet differs from the first")
	}
}

// What changed is read again, and only that: a goblin's new report shows in
// the next snapshot at the cost of its own status file.
func TestASnapshotReadsAgainOnlyTheFileThatChanged(t *testing.T) {
	// Arrange
	s, h := liveSizedFleet(t)
	if _, err := s.Snapshot(); err != nil {
		t.Fatal(err)
	}
	if err := state.AppendStatus(h.State, "goblin-03", "working: the new report"); err != nil {
		t.Fatal(err)
	}
	before := fsx.Opens()

	// Act
	snapshot, err := s.Snapshot()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	const budget = 7
	opened := fsx.Opens() - before
	t.Logf("a snapshot after one goblin's report opened %d files", opened)
	if opened > budget {
		t.Errorf("a snapshot after one goblin's report opened %d files, want at most %d", opened, budget)
	}
	for _, task := range snapshot.Tasks {
		if task.ID == "goblin-03" && task.Activity != "working: the new report" {
			t.Errorf("goblin-03 reads %q, want its new report", task.Activity)
		}
	}
}
