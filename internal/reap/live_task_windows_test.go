package reap

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
)

// The collector reads a working directory only for harness-shaped processes
// and their ancestors, and records the fleet's session and when each worktree
// directory was made.
func TestCollectorPlacesHarnessesAndDatesWorktrees(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".worktrees", "gb-board"), 0o755); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(root, "state")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var read []int
	collector := Collector{
		Home:    home.Home{Root: root, State: stateDir},
		Session: "fleet",
		Processes: stubProcesses{
			process(500, 1, "herdr.exe", `herdr --session ceo-board-test-1 server`, fixtureStart),
			process(510, 500, "powershell.exe", `powershell -NoExit`, fixtureLater),
			process(520, 510, "codex.exe", `codex.exe fixture.json`, fixtureLatest),
			process(900, 1, "node.exe", `node server.js`, fixtureLatest),
		},
		WorkingDirectory: func(pid int) (string, error) {
			read = append(read, pid)
			return `C:\dir\` + string(rune('a'+pid%26)), nil
		},
	}

	inv, _, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(read)
	if !slices.Equal(read, []int{500, 510, 520}) {
		t.Errorf("working directories read for %v, want the stand-in and its ancestors only", read)
	}
	if inv.Session != "fleet" {
		t.Errorf("session = %q, want fleet", inv.Session)
	}
	for _, process := range inv.Processes {
		if process.PID == 520 && process.Cwd == "" {
			t.Error("the stand-in's working directory was not recorded")
		}
	}
	if len(inv.Worktrees) != 1 || inv.Worktrees[0].Created.IsZero() || time.Since(inv.Worktrees[0].Created) > time.Hour {
		t.Errorf("worktrees = %+v, want one dated just now", inv.Worktrees)
	}
}
