package reap

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestWorktreesScansTheProjectsRootForGoblinWorktreesOnly(t *testing.T) {
	root := t.TempDir()
	projects := filepath.Join(root, "dev")
	known := filepath.Join(projects, "known")
	for _, dir := range []string{
		filepath.Join(root, "home"),
		filepath.Join(known, ".worktrees", "gb-live"),
		filepath.Join(known, ".worktrees", "stray"),
		filepath.Join(projects, "forgotten", ".worktrees", "gb-archived"),
		filepath.Join(projects, "forgotten", ".worktrees", "operators-own"),
		filepath.Join(projects, "untouched"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	collector := Collector{
		Home:         home.Home{Root: filepath.Join(root, "home")},
		ProjectsRoot: func() (string, error) { return projects, nil },
	}
	tasks := []Task{{ID: "live", Meta: state.TaskMeta{Project: known}}}

	var notes []string
	var got []string
	for _, worktree := range collector.worktrees(tasks, &notes) {
		got = append(got, worktree.Path)
	}
	sort.Strings(got)
	want := []string{
		// A project a task names reports everything, exactly as before.
		filepath.Join(known, ".worktrees", "gb-live"),
		filepath.Join(known, ".worktrees", "stray"),
		// A checkout no task names is reached through the projects root, and
		// only its goblin worktree is the fleet's to report.
		filepath.Join(projects, "forgotten", ".worktrees", "gb-archived"),
	}
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("worktrees = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("worktrees = %q, want %q", got, want)
			break
		}
	}
	if len(notes) != 0 {
		t.Errorf("unexpected notes: %q", notes)
	}

	collector.ProjectsRoot = nil
	if found := collector.worktrees(tasks, &notes); len(found) != 2 {
		t.Errorf("without a projects root the scan found %d worktrees, want the known project's 2", len(found))
	}
}
