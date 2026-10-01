package reap

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
)

const (
	liveWorktree = `C:\dev\proj\.worktrees\gb-board`
	extraDir     = `C:\dev\proj\.worktrees\gb-board-5b`
)

// fleetWithGoblin is one goblin, board, working in its worktree. alive says
// whether its native terminal still runs.
func fleetWithGoblin(alive bool, verb string) Inventory {
	inv := Inventory{
		Tasks:     []Task{task("board", liveWorktree, verb)},
		Worktrees: []WorktreeDir{{Path: liveWorktree, Project: `C:\dev\proj`, TaskID: "board", Registration: RegistrationListed, Created: fixtureStart}},
	}
	if alive {
		inv = withTerminal(inv, "board", 200)
	}
	return inv
}

// A gate's test step can run a scratch home from the worktree it validates.
// That fixture is in use while a gate agent still works there, even once the
// goblin is gone, and a leak once none does.
func TestAScratchHomeOfALiveGateRunIsNotAnOrphan(t *testing.T) {
	inv := withScratchHost(fleetWithGoblin(false, "done: PR https://example.invalid/pull/1"), liveWorktree, scratchHome+`\state`)
	gate := process(800, 1, "no-mistakes.exe", `no-mistakes daemon`, fixtureStart)
	agent := process(810, 800, "claude.exe", `claude --model opus -p --json-schema {}`, fixtureLater)
	agent.Cwd = liveWorktree
	live := inv
	live.Processes = append(slices.Clone(inv.Processes), gate, agent)

	if orphans := classOf(Classify(live), OrphanProcess); len(orphans) != 0 {
		t.Fatalf("a live gate run's fixture was reported: %v", lines(orphans))
	}

	finished := inv
	finished.Processes = append(slices.Clone(inv.Processes), gate)
	if orphans := classOf(Classify(finished), OrphanProcess); len(orphans) != 2 {
		t.Fatalf("orphans after the gate run finished = %v, want the scratch host and its harness", lines(orphans))
	}
}

// The tie runs only through the goblin's own terminal. A harness left running
// in a live goblin's worktree outside that terminal is the unsupervised,
// token-spending orphan this sweep exists for, and working in the right
// directory must not hide it.
func TestAHarnessInALiveGoblinsWorktreeOutsideItsTerminalIsStillAnOrphan(t *testing.T) {
	inv := fleetWithGoblin(true, "working")
	stray := process(700, 1, "claude.exe", `claude --dangerously-skip-permissions --continue`, fixtureLatest)
	stray.Cwd = liveWorktree
	inv.Processes = append(inv.Processes, stray)

	orphans := classOf(Classify(inv), OrphanProcess)
	if len(orphans) != 1 || orphans[0].PID != 700 {
		t.Fatalf("orphans = %v, want the stray harness pid 700", lines(orphans))
	}
}

func TestALiveGoblinsExtraWorktreeIsNotAnOrphan(t *testing.T) {
	inv := fleetWithGoblin(true, "working")
	inv.Worktrees = append(inv.Worktrees, WorktreeDir{Path: extraDir, Project: `C:\dev\proj`, TaskID: "board-5b", Registration: RegistrationListed, Created: fixtureLater})

	if orphans := classOf(Classify(inv), OrphanWorktree); len(orphans) != 0 {
		t.Fatalf("a live goblin's extra worktree was reported: %v", lines(orphans))
	}
}

// With its goblin gone the extra worktree is reported, held on the goblin's
// own unfinished status, and under its own id: acting on it must return that
// directory alone and never clean up the goblin's task.
func TestAnExtraWorktreeOfARetiredGoblinIsReportedUnderItsOwnID(t *testing.T) {
	inv := fleetWithGoblin(false, "working")
	inv.Worktrees = append(inv.Worktrees, WorktreeDir{Path: extraDir, Project: `C:\dev\proj`, TaskID: "board-5b", Registration: RegistrationListed, Created: fixtureLater})

	var extra *Finding
	findings := classOf(Classify(inv), OrphanWorktree)
	for i := range findings {
		if findings[i].Path == extraDir {
			extra = &findings[i]
		}
	}
	if extra == nil {
		t.Fatalf("the retired goblin's extra worktree was not reported: %v", lines(findings))
	}
	if extra.TaskID != "board-5b" {
		t.Errorf("task id = %q, want the directory's own board-5b", extra.TaskID)
	}
	if !strings.Contains(extra.Detail, "extra worktree of task board") {
		t.Errorf("detail %q does not name the goblin it belongs to", extra.Detail)
	}
	if hold := extra.Hold(); !strings.Contains(hold, "Name board with --force") {
		t.Errorf("hold %q is not keyed to the goblin's own task", hold)
	}
}

// A directory older than the live task whose name it extends is not that
// task's: it is a leftover of some earlier task and is reported as before.
func TestADirectoryOlderThanTheTaskItsNameExtendsIsNotTiedToIt(t *testing.T) {
	inv := fleetWithGoblin(true, "working")
	inv.Worktrees = append(inv.Worktrees, WorktreeDir{Path: extraDir, Project: `C:\dev\proj`, TaskID: "board-5b", Registration: RegistrationListed, Created: fixtureStart.Add(-time.Hour)})

	orphans := classOf(Classify(inv), OrphanWorktree)
	if len(orphans) != 1 || orphans[0].Path != extraDir || strings.Contains(orphans[0].Detail, "extra worktree") {
		t.Fatalf("orphans = %v, want the older directory reported with no owner", lines(orphans))
	}
}

func TestADevServerInALiveGoblinsExtraWorktreeIsNotStale(t *testing.T) {
	inv := fleetWithGoblin(true, "working")
	inv.Worktrees = append(inv.Worktrees, WorktreeDir{Path: extraDir, Project: `C:\dev\proj`, TaskID: "board-5b", Registration: RegistrationListed, Created: fixtureLater})
	inv.Processes = append(inv.Processes, process(800, 1, "node.exe", `node `+extraDir+`\node_modules\vite\bin\vite.js`, fixtureLatest))

	if stale := classOf(Classify(inv), StaleServer); len(stale) != 0 {
		t.Fatalf("a dev server in a live goblin's extra worktree was reported: %v", lines(stale))
	}
}

// The collector reads a working directory only for harness-shaped processes
// and their ancestors, and records when each worktree directory was made.
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
		Home: home.Home{Root: root, State: stateDir},
		Processes: stubProcesses{
			process(500, 1, "cfo-new.exe", `cfo-new.exe host --state C:\proof\state --id cfo -- powershell.exe`, fixtureStart),
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
	for _, process := range inv.Processes {
		if process.PID == 520 && process.Cwd == "" {
			t.Error("the stand-in's working directory was not recorded")
		}
	}
	if len(inv.Worktrees) != 1 || inv.Worktrees[0].Created.IsZero() || time.Since(inv.Worktrees[0].Created) > time.Hour {
		t.Errorf("worktrees = %+v, want one dated just now", inv.Worktrees)
	}
}

type stubProcesses []Process

func (s stubProcesses) List(context.Context) ([]Process, error) {
	return slices.Clone([]Process(s)), nil
}

func lines(findings []Finding) []string {
	out := make([]string, 0, len(findings))
	for _, finding := range findings {
		out = append(out, finding.Line())
	}
	return out
}
