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
		owner                         string
		isDetached, isService, isTurn bool
	}
	got := map[int]standing{}
	for _, process := range placed {
		got[process.PID] = standing{process.Owner, process.Detached, process.Service, process.ByHarness}
		if process.Command != commands[process.PID] {
			t.Errorf("process %d runs %q, want %q", process.PID, process.Command, commands[process.PID])
		}
	}
	want := map[int]standing{
		102: {"tree", false, false, true}, 103: {"tree", false, false, false}, 104: {"tree", false, false, false},
		105: {"tree", false, false, true}, 106: {"tree", false, false, false}, 107: {"tree", false, false, true},
		400: {"tree", true, true, false}, 401: {"tree", true, true, false},
		601: {"cfo", false, false, true},
	}
	if !maps.Equal(got, want) {
		t.Errorf("placed = %v, want %v", got, want)
	}
	if pids := slices.Sorted(maps.Keys(reads)); slices.ContainsFunc(pids, func(pid int) bool { return reads[pid] != 1 }) {
		t.Errorf("command lines read %v times over two readings, want each once", reads)
	}
}
