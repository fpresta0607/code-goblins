package fleettree

import (
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// The start watch reads every few seconds what each goblin and the CFO's
// terminal started, placed as the family tree places a goblin's processes:
// each job its harness started after launching, with what runs under it,
// and each detached tree that is its own. Never its harness, its MCP
// servers or a program of nobody's.
func TestPlacedPlacesWhatEachOwnerStarted(t *testing.T) {
	// Arrange
	launched := at.Add(-time.Hour)
	later := func(minutes int) time.Time { return launched.Add(time.Duration(minutes) * time.Minute) }
	processes := append(goblinProcesses(launched, 0),
		Process{PID: 600, ParentPID: 1, Exe: "claude.exe", Created: 60, Started: launched},
		Process{PID: 601, ParentPID: 600, Exe: "bash.exe", Created: 61, Started: later(30)},
		Process{PID: 400, ParentPID: 905, Exe: "no-mistakes.exe", Created: 40, Started: later(50)},
		Process{PID: 401, ParentPID: 400, Exe: "claude.exe", Created: 41, Started: later(55)},
		Process{PID: 230, ParentPID: 903, Exe: "explorer.exe", Created: 25, Started: later(-600)},
	)
	commands := maps.Clone(goblinCommands)
	commands[400], commands[601] = "no-mistakes.exe daemon", `bash -c "eval 'cfo drain'"`
	reads := map[int]int{}
	reader := Reader{
		Processes:   func() ([]Process, error) { return processes, nil },
		CommandLine: func(pid int) (string, error) { reads[pid]++; return commands[pid], nil },
		Owner:       func(pid int) string { return map[int]string{400: "tree"}[pid] },
	}
	owners := []Goblin{
		{Meta: state.TaskMeta{ID: "tree"}, HarnessPID: 100},
		{Meta: state.TaskMeta{ID: "cfo"}, HarnessPID: 600},
		{Meta: state.TaskMeta{ID: "gone"}, HarnessPID: 700},
	}

	// Act: two readings.
	if _, err := reader.Placed(owners); err != nil {
		t.Fatal(err)
	}
	placed, err := reader.Placed(owners)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	type standing struct {
		owner                                string
		isDetached, isService, isTurn, isRun bool
	}
	got := map[int]standing{}
	for _, process := range placed {
		got[process.PID] = standing{process.Owner, process.Detached, process.Service, process.ByHarness, process.InRun}
		if process.Command != commands[process.PID] {
			t.Errorf("process %d runs %q, want %q", process.PID, process.Command, commands[process.PID])
		}
	}
	want := map[int]standing{
		102: {"tree", false, false, true, true}, 103: {"tree", false, false, false, true}, 104: {"tree", false, false, false, true},
		105: {"tree", false, false, true, false}, 106: {"tree", false, false, false, false}, 107: {"tree", false, false, true, false},
		400: {"tree", true, true, false, false}, 401: {"tree", true, true, false, false},
		601: {"cfo", false, false, true, false},
	}
	if !maps.Equal(got, want) {
		t.Errorf("placed = %v, want %v", got, want)
	}
	if pids := slices.Sorted(maps.Keys(reads)); slices.ContainsFunc(pids, func(pid int) bool { return reads[pid] != 1 }) {
		t.Errorf("command lines read %v times over two readings, want each once", reads)
	}
}

// A test run in the machine's heavy line starts stand-ins such as
// cmd /c "ping -n 30 127.0.0.1 >NUL" and ends the cmd, which leaves the
// ping running with no parent: on 2026-10-14 ten such pings in 45 seconds
// woke the CFO as one goblin's loop. Without its parent nothing says what
// started it but the turn its environment names (verify.TurnVariable), which
// every process of the run inherits, so a process that names one is in a
// run. A detached process that names none is not.
func TestPlacedPutsWhatAHeavyLineRunLeftWithoutAParentInTheRun(t *testing.T) {
	// Arrange
	launched := at.Add(-time.Hour)
	later := func(minutes int) time.Time { return launched.Add(time.Duration(minutes) * time.Minute) }
	processes := append(goblinProcesses(launched, 0),
		Process{PID: 300, ParentPID: 906, Exe: "PING.EXE", Created: 30, Started: later(50)},
		Process{PID: 310, ParentPID: 907, Exe: "node.exe", Created: 31, Started: later(51)},
	)
	commands := maps.Clone(goblinCommands)
	commands[300], commands[310] = "ping -n 30 127.0.0.1", "node server.js"
	asked := map[int]int{}
	reader := Reader{
		Processes:   func() ([]Process, error) { return processes, nil },
		CommandLine: func(pid int) (string, error) { return commands[pid], nil },
		Owner:       func(pid int) string { return map[int]string{300: "tree", 310: "tree"}[pid] },
		InTurn:      func(pid int) bool { asked[pid]++; return pid == 300 },
	}
	owners := []Goblin{{Meta: state.TaskMeta{ID: "tree"}, HarnessPID: 100}}

	// Act: two readings.
	if _, err := reader.Placed(owners); err != nil {
		t.Fatal(err)
	}
	placed, err := reader.Placed(owners)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	inRun := map[int]bool{}
	for _, process := range placed {
		inRun[process.PID] = process.InRun
	}
	if !inRun[300] || inRun[310] {
		t.Errorf("in a run: the ping %v and the server %v, want the ping in the heavy line's run and the server in none", inRun[300], inRun[310])
	}
	if asked[300] != 1 || asked[310] != 1 {
		t.Errorf("asked whether each runs in a turn %v times over two readings, want once in each process's life", asked)
	}
}
