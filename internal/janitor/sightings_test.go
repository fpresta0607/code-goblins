package janitor

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lifecycle"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/reap"
)

// listed are processes as the orphan sweep's own listing gives them: by
// pid, parent, name, command line and start.
type listed []reap.Process

func (l listed) List(context.Context) ([]reap.Process, error) { return slices.Clone(l), nil }

// The orphan sweep and the process sweep ask one question of a process,
// whose it is, and must give one answer: on 2026-10-10 the process plan gave
// every process of a goblin's spawn test to that goblin's terminal while the
// orphan sweep, which knew a harness by its name and its lack of a pane,
// woke the CFO for three of them as orphans. What the orphan sweep gives a
// running terminal is exactly what the process sweep would end as that
// terminal's own were the terminal gone, and the orphan sweep's findings are
// the harnesses the process sweep gives no running terminal.
func TestTheOrphanSweepAndTheProcessSweepGiveAProcessTheSameOwner(t *testing.T) {
	// Arrange
	root := t.TempDir()
	h := home.Home{Root: root, State: filepath.Join(root, "state")}
	worktree := filepath.Join(h.Worktrees(), "proj", "g1")
	scratch := filepath.Join(h.Scratch(), "g1")
	testTmp := filepath.Join(scratch, "TestANativeGoblinStarts1")
	elsewhere := t.TempDir()
	started := time.Date(2026, 10, 10, 11, 12, 0, 0, time.UTC)
	at := func(seconds int) time.Time { return started.Add(time.Duration(seconds) * time.Second) }
	mark := func(terminal string) lifecycle.Mark {
		return lifecycle.Mark{Terminal: terminal, ProofSum: terminal + "-digest"}
	}
	testBinary := filepath.Join(scratch, "go-build1", "b001", "spawn.test.exe")
	standIn := []string{`C:\WINDOWS\system32\cmd.exe`, "/c", "codex", "--dangerously-bypass-approvals-and-sandbox"}
	process := func(pid, parent int, name string, start time.Time, from lifecycle.Process) Process {
		from.PID, from.ParentPID, from.Name, from.Started = pid, parent, name, start
		return Process{Process: from}
	}
	processes := []Process{
		// Terminal g1, its goblin and a shell of the goblin's.
		process(20, 1, "cfo.exe", at(0), lifecycle.Process{Arguments: []string{"cfo.exe", "host", "--id", "g1"}}),
		process(21, 20, "claude.exe", at(1), lifecycle.Process{Directory: worktree, Arguments: []string{"claude", "--dangerously-skip-permissions"}, Mark: mark("g1")}),
		process(22, 21, "bash.exe", at(2), lifecycle.Process{Directory: worktree, Mark: mark("g1")}),
		// The goblin's go test, started by a Git Bash process that has exited.
		process(30, 900, "go.exe", at(3), lifecycle.Process{Directory: worktree, Arguments: []string{"go", "test", "./internal/spawn"}, Mark: mark("g1")}),
		process(31, 30, "spawn.test.exe", at(4), lifecycle.Process{Directory: filepath.Join(worktree, "internal", "spawn"), Arguments: []string{testBinary, "-test.timeout=10m0s"}, Mark: mark("g1")}),
		// The host of the terminal the test started, with no terminal's mark,
		// the cmd in it and the stand-in codex, with the mark of a terminal of
		// the test's own home.
		process(32, 31, "spawn.test.exe", at(5), lifecycle.Process{Directory: filepath.Join(worktree, "internal", "spawn"), Arguments: append([]string{testBinary, "native-spawn-host", "--id", "task-7", "--"}, standIn...)}),
		process(33, 32, "cmd.exe", at(6), lifecycle.Process{Directory: filepath.Join(testTmp, "003"), Arguments: standIn, Mark: mark("task-7")}),
		process(34, 33, "codex.exe", at(7), lifecycle.Process{Directory: filepath.Join(testTmp, "003"), Arguments: standIn[2:], Mark: mark("task-7")}),
		// The harness a terminal that is gone left behind.
		process(40, 901, "codex.exe", at(8), lifecycle.Process{Directory: elsewhere, Arguments: []string{"codex", "--dangerously-bypass-approvals-and-sandbox"}, Mark: mark("g2")}),
		// A harness nothing ties to any terminal.
		process(50, 902, "claude.exe", at(9), lifecycle.Process{Directory: elsewhere, Arguments: []string{"claude", "--dangerously-skip-permissions"}}),
		// The CFO's terminal and its harness.
		process(60, 1, "cfo.exe", at(0), lifecycle.Process{Arguments: []string{"cfo.exe", "host", "--id", "cfo"}}),
		process(61, 60, "claude.exe", at(1), lifecycle.Process{Directory: root, Arguments: []string{"claude"}, Mark: mark("cfo")}),
	}
	owners := []Owner{
		{ID: "cfo", Marks: []lifecycle.Mark{mark("cfo")}, HostPID: 60},
		{ID: "g1", Marks: []lifecycle.Mark{mark("g1")}, Directories: []string{worktree, scratch}, HostPID: 20},
		{ID: "g2", Marks: []lifecycle.Mark{mark("g2")}},
	}
	running := make([]proc.Entry, 0, len(processes))
	rows := make(listed, 0, len(processes))
	for _, process := range processes {
		running = append(running, proc.Entry{PID: process.PID, ParentPID: process.ParentPID, ExeBase: process.Name, Start: process.Started})
		rows = append(rows, reap.Process{PID: process.PID, ParentPID: process.ParentPID, Name: process.Name, CommandLine: strings.Join(process.Arguments, " "), Start: process.Started})
	}

	// Act: the orphan sweep's second reading and its findings, and what the
	// process sweep would end as each running terminal's own were it gone.
	sighted := sightings(running, processes, owners)
	inv, _, err := reap.Collector{Home: h, Session: "default", Processes: rows, Sightings: func(context.Context) ([]reap.Sighting, []string, error) {
		return sighted, nil, nil
	}}.Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var orphans []int
	for _, finding := range reap.Classify(inv) {
		if finding.Class == reap.OrphanProcess {
			orphans = append(orphans, finding.PID)
		}
	}
	slices.Sort(orphans)
	swept := map[string][]int{}
	for _, process := range inv.Processes {
		if process.Owner != "" {
			swept[process.Owner] = append(swept[process.Owner], process.PID)
		}
	}
	planned := map[string][]int{}
	for index, owner := range owners {
		if owner.HostPID == 0 {
			continue
		}
		gone := slices.Clone(owners)
		gone[index].HostPID = 0
		// With its host gone nothing is reached from it, so the host itself
		// is left out as the terminal's teardown would have ended it first.
		remaining := slices.DeleteFunc(slices.Clone(processes), func(process Process) bool { return process.PID == owner.HostPID })
		ending, _, _, _ := planProcesses(remaining, gone, nil, at(60))
		for _, item := range ending {
			if item.Owner == owner.ID {
				planned[owner.ID] = append(planned[owner.ID], item.PID)
			}
		}
	}

	// Assert
	for _, owner := range []string{"cfo", "g1"} {
		slices.Sort(swept[owner])
		slices.Sort(planned[owner])
		if !slices.Equal(swept[owner], planned[owner]) {
			t.Errorf("terminal %s: the orphan sweep gives it pids %v and the process sweep would end pids %v as its own, want the same", owner, swept[owner], planned[owner])
		}
	}
	if want := []int{30, 31, 32, 33, 34}; !slices.Equal(swept["g1"], append([]int{21, 22}, want...)) {
		t.Errorf("the orphan sweep gives terminal g1 pids %v, want its goblin, its shell and its test's %v", swept["g1"], want)
	}
	if len(swept["g2"]) != 0 {
		t.Errorf("the orphan sweep gives pids %v to terminal g2, whose host is gone", swept["g2"])
	}
	if !slices.Equal(orphans, []int{40, 50}) {
		t.Errorf("the orphan sweep reports pids %v, want the gone terminal's harness and the one nothing owns, 40 and 50", orphans)
	}
	if index := slices.IndexFunc(inv.Processes, func(process reap.Process) bool { return process.PID == 40 }); index < 0 || inv.Processes[index].Terminal != "g2" {
		t.Errorf("the gone terminal's harness is not known by its terminal's mark: %+v", inv.Processes)
	}
}

// A process the second reading could not read still runs, so it is sighted
// and owned by nobody, never dropped: a harness the sweep cannot read is a
// finding, where one that ended is not. A gate's agent carries the mark of
// whichever goblin started the daemon, which makes it neither that
// terminal's own nor known by its mark.
func TestASightingSaysOnlyWhatWasRead(t *testing.T) {
	// Arrange
	started := time.Date(2026, 10, 10, 11, 12, 0, 0, time.UTC)
	mark := lifecycle.Mark{Terminal: "g1", ProofSum: "g1-digest"}
	owners := []Owner{{ID: "g1", Marks: []lifecycle.Mark{mark}, HostPID: 20}}
	running := []proc.Entry{
		{PID: 21, ParentPID: 20, ExeBase: "claude.exe", Start: started},
		{PID: 30, ParentPID: 900, ExeBase: "codex.exe", Start: started},
		{PID: 31, ParentPID: 901, ExeBase: "codex.exe", Start: started.Add(time.Minute)},
		{PID: 32, ParentPID: 902, ExeBase: "codex.exe", Start: started},
	}
	processes := []Process{
		{Process: lifecycle.Process{PID: 21, ParentPID: 20, Name: "claude.exe", Started: started, Mark: mark}},
		// Read before its pid was given to the process that runs now.
		{Process: lifecycle.Process{PID: 31, ParentPID: 901, Name: "codex.exe", Started: started, Mark: mark}},
		{Process: lifecycle.Process{PID: 32, ParentPID: 902, Name: "codex.exe", Started: started, Mark: mark, IsGateAgent: true}},
	}

	// Act
	sighted := sightings(running, processes, owners)

	// Assert
	want := []reap.Sighting{
		{PID: 21, Started: started, Terminal: "g1", Owner: "g1"},
		{PID: 30, Started: started},
		{PID: 31, Started: started.Add(time.Minute)},
		{PID: 32, Started: started, IsGateAgent: true},
	}
	if !slices.Equal(sighted, want) {
		t.Errorf("sightings = %+v, want %+v", sighted, want)
	}
}
