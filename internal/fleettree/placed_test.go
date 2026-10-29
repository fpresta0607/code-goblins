package fleettree

import (
	"context"
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

// A link the CFO's harness opens, such as a sign-in's, starts the
// Overlord's default browser under the CFO's terminal when none runs, and
// that browser is his from then on, with every tab and native messaging host
// it starts. On 2026-10-16 the start watch blamed four of its windows on the
// CFO's terminal and advised ending them by pid (#1007). A browser a goblin
// runs for its work is still the goblin's.
func TestPlacedLeavesABrowserALinkOpenedToTheOverlord(t *testing.T) {
	// Arrange
	launched := at.Add(-time.Hour)
	later := func(minutes int) time.Time { return launched.Add(time.Duration(minutes) * time.Minute) }
	processes := append(goblinProcesses(launched, 0),
		Process{PID: 600, ParentPID: 1, Exe: "claude.exe", Created: 60, Started: launched},
		Process{PID: 601, ParentPID: 600, Exe: "node.exe", Created: 61, Started: later(30)},
		Process{PID: 610, ParentPID: 601, Exe: "chrome.exe", Created: 62, Started: later(31)},
		Process{PID: 611, ParentPID: 610, Exe: "chrome.exe", Created: 63, Started: later(31)},
		Process{PID: 612, ParentPID: 610, Exe: "cmd.exe", Created: 64, Started: later(32)},
		Process{PID: 613, ParentPID: 612, Exe: "conhost.exe", Created: 65, Started: later(32)},
		Process{PID: 620, ParentPID: 9999, Exe: "msedge.exe", Created: 66, Started: later(40)},
		Process{PID: 621, ParentPID: 620, Exe: "msedge.exe", Created: 67, Started: later(40)},
	)
	commands := maps.Clone(goblinCommands)
	commands[601] = `node.exe open.js`
	commands[610] = `"C:\Program Files\Google\Chrome\Application\chrome.exe" --single-argument https://claude.com/cai/oauth/authorize`
	commands[611] = `"C:\Program Files\Google\Chrome\Application\chrome.exe" --type=renderer`
	commands[612] = `C:\WINDOWS\system32\cmd.exe /d /s /c ""C:\hosts\host.exe" chrome-extension://abc/ --parent-window=0"`
	commands[613] = `\??\C:\WINDOWS\system32\conhost.exe 0x4`
	commands[620] = `"C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe" --single-argument https://example.com/login`
	commands[621] = `"C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe" --type=gpu-process`
	reader := Reader{
		Processes:   func() ([]Process, error) { return processes, nil },
		CommandLine: func(pid int) (string, error) { return commands[pid], nil },
		Owner:       func(pid int) string { return map[int]string{620: "cfo", 621: "cfo"}[pid] },
	}
	owners := []Goblin{
		{Meta: state.TaskMeta{ID: "tree"}, HarnessPID: 100},
		{Meta: state.TaskMeta{ID: "cfo"}, HarnessPID: 600},
	}

	// Act
	placed, err := reader.Placed(owners)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	owner := map[int]string{}
	for _, process := range placed {
		owner[process.PID] = process.Owner
	}
	for _, pid := range []int{610, 611, 612, 613, 620, 621} {
		if mine, isPlaced := owner[pid]; isPlaced {
			t.Errorf("process %d (%s) placed as %s's, want the Overlord's browser left to him", pid, commands[pid], mine)
		}
	}
	if owner[601] != "cfo" || owner[107] != "tree" {
		t.Errorf("placed = %v, want the CFO's tool call and the goblin's own browser still placed", owner)
	}
}

// The supervisor is the fleet's own service, and nothing it runs is an
// owner's start. On 2026-10-25 the start watch woke the CFO for its
// terminal's loop of no-mistakes axi sync --check: the supervisor's own
// gate-status reads, under a supervisor an update the CFO's terminal ran had
// started. Its console host still carried that terminal's mark, and a read
// in a goblin's worktree is that goblin's by its place, so whichever of them
// answers first took the supervisor's whole tree (#1499). A supervisor that a
// tool call of the CFO's still runs above is no work of the call's either.
func TestPlacedPlacesNothingTheSupervisorRuns(t *testing.T) {
	for _, test := range []struct {
		name   string
		owners map[int]string
	}{
		{"its console carries the CFO terminal's mark", map[int]string{801: "cfo", 802: "tree", 812: "tree"}},
		{"its console carries no mark", map[int]string{802: "tree", 812: "tree"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			launched := at.Add(-time.Hour)
			later := func(minutes int) time.Time { return launched.Add(time.Duration(minutes) * time.Minute) }
			processes := append(goblinProcesses(launched, 0),
				Process{PID: 600, ParentPID: 1, Exe: "claude.exe", Created: 60, Started: launched},
				// A supervisor whose starter has exited, with its console and a
				// gate-status read in the goblin's worktree.
				Process{PID: 800, ParentPID: 908, Exe: "goblins.exe", Created: 80, Started: later(40)},
				Process{PID: 801, ParentPID: 800, Exe: "conhost.exe", Created: 81, Started: later(40)},
				Process{PID: 802, ParentPID: 800, Exe: "no-mistakes.exe", Created: 82, Started: later(50)},
				Process{PID: 803, ParentPID: 802, Exe: "no-mistakes.exe", Created: 83, Started: later(50)},
				// An update the CFO's tool call still runs, and the supervisor it
				// started reading the same.
				Process{PID: 610, ParentPID: 600, Exe: "bash.exe", Created: 61, Started: later(45)},
				Process{PID: 611, ParentPID: 610, Exe: "goblins.exe", Created: 62, Started: later(45)},
				Process{PID: 811, ParentPID: 611, Exe: "goblins.exe", Created: 63, Started: later(46)},
				Process{PID: 812, ParentPID: 811, Exe: "no-mistakes.exe", Created: 64, Started: later(50)},
			)
			commands := maps.Clone(goblinCommands)
			commands[800] = `C:\dev\code-goblins\bin\goblins.exe serve --listen 127.0.0.1:4310`
			commands[801] = `\??\C:\WINDOWS\system32\conhost.exe 0x4`
			commands[802] = `no-mistakes axi sync --check`
			commands[803] = `no-mistakes --update-check`
			commands[610] = `bash -c "eval 'goblins update'"`
			commands[611] = `C:\dev\code-goblins\bin\goblins.exe update`
			commands[811] = `C:\dev\code-goblins\update\previous-goblins.exe serve --listen 127.0.0.1:4310`
			commands[812] = `no-mistakes axi sync --check`
			reader := Reader{
				Processes:   func() ([]Process, error) { return processes, nil },
				CommandLine: func(pid int) (string, error) { return commands[pid], nil },
				Owner:       func(pid int) string { return test.owners[pid] },
			}
			owners := []Goblin{
				{Meta: state.TaskMeta{ID: "tree"}, HarnessPID: 100},
				{Meta: state.TaskMeta{ID: "cfo"}, HarnessPID: 600},
			}

			// Act
			placed, err := reader.Placed(owners)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			owner := map[int]string{}
			for _, process := range placed {
				owner[process.PID] = process.Owner
			}
			for _, pid := range []int{800, 801, 802, 803, 811, 812} {
				if mine, isPlaced := owner[pid]; isPlaced {
					t.Errorf("process %d (%s) placed as %s's, want the supervisor's own work placed as nobody's", pid, commands[pid], mine)
				}
			}
			if owner[610] != "cfo" || owner[611] != "cfo" || owner[107] != "tree" {
				t.Errorf("placed = %v, want the CFO's update call and the goblin's own work still placed", owner)
			}
		})
	}
}

// The Overlord's browser a link opened under a goblin's harness is his, so
// its memory is none of the goblin's either, though the opener that started
// it is the goblin's job and still runs (#1007).
func TestReadLeavesTheMemoryOfABrowserALinkOpenedOutOfTheGoblins(t *testing.T) {
	// Arrange
	launched := at.Add(-time.Hour)
	processes := append(goblinProcesses(launched, 0),
		Process{PID: 601, ParentPID: 100, Exe: "node.exe", Created: 61, Started: launched.Add(50 * time.Minute), Memory: 40 * megabyte},
		Process{PID: 610, ParentPID: 601, Exe: "chrome.exe", Created: 62, Started: launched.Add(51 * time.Minute), Memory: 900 * megabyte},
		Process{PID: 611, ParentPID: 610, Exe: "chrome.exe", Created: 63, Started: launched.Add(51 * time.Minute), Memory: 600 * megabyte},
	)
	commands := maps.Clone(goblinCommands)
	commands[601] = `node.exe open.js`
	commands[610] = `"C:\Program Files\Google\Chrome\Application\chrome.exe" --single-argument https://claude.com/cai/oauth/authorize`
	commands[611] = `"C:\Program Files\Google\Chrome\Application\chrome.exe" --type=renderer`
	reader := Reader{
		Home:        t.TempDir(),
		Processes:   func() ([]Process, error) { return processes, nil },
		Listeners:   func() (map[int][]int, error) { return map[int][]int{}, nil },
		CommandLine: func(pid int) (string, error) { return commands[pid], nil },
		Now:         func() time.Time { return at },
	}

	// Act
	tree, err := reader.Read(context.Background(), Goblin{Meta: state.TaskMeta{ID: "tree", Harness: "codex"}, HarnessPID: 100})

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if want := uint64(500+80+5+300+200+3+150+400+40) * megabyte; tree.Memory != want {
		t.Errorf("memory = %d MB, want %d MB: the harness and everything under it but the Overlord's browser", tree.Memory/megabyte, want/megabyte)
	}
}
