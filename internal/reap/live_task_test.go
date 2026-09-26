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
	fixtureRoot  = `C:\Users\op\AppData\Local\Temp\cfo-board-windows-0a1b`
	gateWorktree = `C:\Users\op\.no-mistakes\worktrees\a3a2\01M3RUN`
)

// fleetWithGoblin is one goblin, board, working in its worktree through the
// fleet's own Herdr session. alive says whether its pane still holds an agent.
func fleetWithGoblin(alive bool, verb string) Inventory {
	pane := Pane{ID: "pane-board", ShellPID: 200, ForegroundPID: 300, HasAgent: alive}
	if alive {
		pane.AgentCwd = liveWorktree
	}
	return Inventory{
		Session:       "default",
		Tasks:         []Task{task("board", liveWorktree, "pane-board", verb)},
		Panes:         []Pane{pane},
		Worktrees:     []WorktreeDir{{Path: liveWorktree, Project: `C:\dev\proj`, TaskID: "board", Registration: RegistrationListed, Created: fixtureStart}},
		FleetRootPIDs: []int{100},
		Processes: []Process{
			process(100, 1, "herdr.exe", `herdr.exe server`, fixtureStart),
			process(200, 100, "powershell.exe", `powershell -NoExit`, fixtureLater),
			process(300, 200, "claude.exe", `claude --dangerously-skip-permissions --strict-mcp-config`, fixtureLatest),
		},
	}
}

// withFixture adds the native board acceptance fixture's shape: a Herdr
// server for its own test session, started from dir by a script that has
// since exited, with a stand-in codex.exe running in one of its panes.
func withFixture(inv Inventory, dir string) Inventory {
	server := process(500, 777, "herdr.exe", `"C:\Programs\Herdr\bin\herdr.exe" --session cfo-board-test-e736 server`, fixtureLater)
	server.Cwd = dir
	shell := process(510, 500, "powershell.exe", `powershell.exe -NoExit -Command herdr-prompt`, fixtureLatest)
	shell.Cwd = fixtureRoot + `\project`
	stub := process(520, 510, "codex.exe", `"`+fixtureRoot+`\bin\codex.exe" "`+fixtureRoot+`\fixture.json"`, fixtureLatest.Add(time.Minute))
	stub.Cwd = fixtureRoot + `\project`
	inv.FleetRootPIDs = append(inv.FleetRootPIDs, 500)
	inv.Processes = append(inv.Processes, server, shell, stub)
	return inv
}

// The 2026-09-24 noise: a live goblin's own board fixture stand-ins read as
// unsupervised harnesses on every sweep, four wakes in one evening.
func TestAStandInUnderALiveGoblinsFixtureIsNotAnOrphan(t *testing.T) {
	findings := Classify(withFixture(fleetWithGoblin(true, "working"), liveWorktree))

	if orphans := classOf(findings, OrphanProcess); len(orphans) != 0 {
		t.Fatalf("a live goblin's fixture stand-in was reported: %v", lines(orphans))
	}
}

// Once the goblin that started it is gone, the fixture is a leak, and it is
// reported against that goblin so the operator knows whose it was.
func TestAStandInOfARetiredGoblinsFixtureIsReportedAgainstThatGoblin(t *testing.T) {
	findings := Classify(withFixture(fleetWithGoblin(false, "done"), liveWorktree))

	orphans := classOf(findings, OrphanProcess)
	if len(orphans) != 1 || orphans[0].PID != 520 {
		t.Fatalf("orphans = %v, want the stand-in pid 520", lines(orphans))
	}
	if orphans[0].TaskID != "board" || orphans[0].Path != liveWorktree {
		t.Errorf("stand-in attributed to %q at %q, want board at %s", orphans[0].TaskID, orphans[0].Path, liveWorktree)
	}
	if !strings.Contains(orphans[0].Detail, "test fixture") || !strings.Contains(orphans[0].Detail, "pid 500") {
		t.Errorf("detail %q does not say it is a test fixture's under Herdr pid 500", orphans[0].Detail)
	}
}

// A goblin's extra worktree is its own too: a fixture it started there is in
// use for as long as the goblin is.
func TestAStandInUnderAFixtureStartedInALiveGoblinsExtraWorktreeIsNotAnOrphan(t *testing.T) {
	inv := fleetWithGoblin(true, "working")
	inv.Worktrees = append(inv.Worktrees, WorktreeDir{Path: extraDir, Project: `C:\dev\proj`, TaskID: "board-5b", Registration: RegistrationListed, Created: fixtureLater})

	findings := Classify(withFixture(inv, extraDir))

	if orphans := classOf(findings, OrphanProcess); len(orphans) != 0 {
		t.Fatalf("a fixture started in the goblin's extra worktree was reported: %v", lines(orphans))
	}
}

// A gate's test step runs the same fixture from the gate's own worktree. It is
// in use while a gate agent still works there, and a leak once none does.
func TestAStandInUnderALiveGateRunsFixtureIsNotAnOrphan(t *testing.T) {
	inv := withFixture(fleetWithGoblin(true, "working"), gateWorktree)
	gate := process(600, 1, "no-mistakes.exe", `no-mistakes daemon`, fixtureStart)
	agent := process(610, 600, "claude.exe", `claude --model opus -p --json-schema {}`, fixtureLater)
	agent.Cwd = gateWorktree
	live := inv
	live.Processes = append(slices.Clone(inv.Processes), gate, agent)

	if orphans := classOf(Classify(live), OrphanProcess); len(orphans) != 0 {
		t.Fatalf("a live gate run's fixture stand-in was reported: %v", lines(orphans))
	}

	finished := inv
	finished.Processes = append(slices.Clone(inv.Processes), gate)
	if orphans := classOf(Classify(finished), OrphanProcess); len(orphans) != 1 || orphans[0].PID != 520 {
		t.Fatalf("orphans after the gate run finished = %v, want the leaked stand-in", lines(orphans))
	}
}

// The tie runs only through a Herdr server of another session. A harness
// left running in a live goblin's worktree after the fleet's own Herdr went
// down is the unsupervised, token-spending orphan this sweep exists for, and
// working in the right directory must not hide it.
func TestAHarnessInALiveGoblinsWorktreeWithNoPaneIsStillAnOrphan(t *testing.T) {
	inv := fleetWithGoblin(true, "working")
	stray := process(700, 100, "claude.exe", `claude --dangerously-skip-permissions --continue`, fixtureLatest)
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

func TestHerdrSessionReadsTheSessionAServerRuns(t *testing.T) {
	for _, tc := range []struct{ command, want string }{
		{`"C:\Herdr\herdr.exe" --session cfo-board-test-e736 server`, "cfo-board-test-e736"},
		{`herdr --session=fleet server`, "fleet"},
		{`herdr.exe server`, "default"},
	} {
		if got := herdrSession(tc.command); got != tc.want {
			t.Errorf("herdrSession(%q) = %q, want %q", tc.command, got, tc.want)
		}
	}
}

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
			process(500, 1, "herdr.exe", `herdr --session cfo-board-test-1 server`, fixtureStart),
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
