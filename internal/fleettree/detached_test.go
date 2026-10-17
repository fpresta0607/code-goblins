package fleettree

import (
	"context"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/verify"
)

// A process that lost its parent is reachable from no harness, so the tree
// dropped it: on 2026-10-09 two browser bridges held 3.7 GB that no card
// showed, and one goblin's 2 hour old server was in no tree. A detached tree
// carries its terminal's mark, and shows under the goblin that mark names
// with its memory, counted in the goblin's total. It is no sign the goblin
// works: a stuck leftover using the processor must not hide a stalled
// goblin.
func TestReadShowsAGoblinsDetachedTreesUnderItWithTheirMemory(t *testing.T) {
	// Arrange
	launched := at.Add(-time.Hour)
	later := func(minutes int) time.Time { return launched.Add(time.Duration(minutes) * time.Minute) }
	processes := append(goblinProcesses(launched, 0),
		// A browser bridge and its browser, started from a shell that exited.
		Process{PID: 200, ParentPID: 900, Exe: "node.exe", Created: 20, Started: later(45), CPU: time.Minute, Memory: 200 * megabyte},
		Process{PID: 201, ParentPID: 200, Exe: "chrome.exe", Created: 21, Started: later(46), CPU: time.Minute, Memory: 2300 * megabyte},
		// A Git Bash shell, which carries no mark, left holding a server.
		Process{PID: 210, ParentPID: 901, Exe: "bash.exe", Created: 22, Started: later(47), Memory: megabyte},
		Process{PID: 211, ParentPID: 210, Exe: "node.exe", Created: 23, Started: later(48), Memory: 99 * megabyte},
		// Another goblin's detached server, and a program that is nobody's.
		Process{PID: 220, ParentPID: 902, Exe: "node.exe", Created: 24, Started: later(49), Memory: 700 * megabyte},
		Process{PID: 230, ParentPID: 903, Exe: "explorer.exe", Created: 25, Started: later(-600), Memory: 300 * megabyte},
	)
	marks := map[int]string{100: "tree", 200: "tree", 201: "tree", 211: "tree", 220: "another"}
	asked := map[int]int{}
	now := at
	reader := Reader{
		Home:        t.TempDir(),
		Processes:   func() ([]Process, error) { return processes, nil },
		Listeners:   func() (map[int][]int, error) { return map[int][]int{106: {5173}, 211: {47391}}, nil },
		CommandLine: func(pid int) (string, error) { return goblinCommands[pid], nil },
		Owner:       func(pid int) string { asked[pid]++; return marks[pid] },
		Now:         func() time.Time { return now },
	}
	goblin := Goblin{Meta: state.TaskMeta{ID: "tree", Harness: "codex"}, HarnessPID: 100}
	attached, err := (&Reader{Home: t.TempDir(), Processes: reader.Processes, Listeners: reader.Listeners, CommandLine: reader.CommandLine, Now: reader.Now}).Read(context.Background(), goblin)
	if err != nil {
		t.Fatal(err)
	}

	// Act
	tree, err := reader.Read(context.Background(), goblin)
	if err != nil {
		t.Fatal(err)
	}
	now = at.Add(time.Minute)
	if _, err := reader.Read(context.Background(), goblin); err != nil {
		t.Fatal(err)
	}

	// Assert
	detached := map[string]Node{}
	for _, child := range tree.Children {
		if child.Detached {
			detached[child.ID] = child
		}
	}
	bridge, server := detached["process:200:20"], detached["process:210:22"]
	if len(detached) != 2 || bridge.Memory != 2500*megabyte || server.Memory != 100*megabyte {
		t.Fatalf("detached trees = %+v, want the bridge with its browser at 2500 MB and the shell with its server at 100 MB, and neither another goblin's nor nobody's", detached)
	}
	if bridge.Group != GroupBrowser || server.Group != GroupDevServer {
		t.Errorf("the bridge is a %q and the server a %q, want a browser and a dev server", bridge.Group, server.Group)
	}
	if want := attached.Memory + 2600*megabyte; tree.Memory != want {
		t.Errorf("the goblin's memory = %d MB, want its harness's tree and its detached trees, %d MB", tree.Memory/megabyte, want/megabyte)
	}
	names, _ := tree.Jobs()
	if slices.ContainsFunc(names, func(name string) bool { return strings.Contains(name, "pid 200") || strings.Contains(name, "pid 210") }) {
		t.Errorf("the goblin's jobs %v name a detached tree, which is no sign that it works", names)
	}
	if attached.Working() != tree.Working() {
		t.Errorf("the goblin reads as working = %t with its detached trees and %t without, want the same", tree.Working(), attached.Working())
	}
	for pid, times := range asked {
		if times != 1 {
			t.Errorf("process %d was asked whose it is %d times over two reads, want once", pid, times)
		}
	}
	if asked[100] != 0 || asked[106] != 0 {
		t.Errorf("processes under the harness were asked whose they are (%v), want only those that reach no harness", asked)
	}
}

// On 2026-10-11 the supervisor twice raised progress_stalled on a goblin
// whose whole test suite ran for hours. Git Bash had cut the suite off from
// the harness, its first processes were Git Bash's own, which carry no mark,
// the goblin's script had unset the mark for the rest, and the reader kept
// the first answer, nobody's, for the tree's whole life. A process that joins
// a tree is asked whose it is too: here the goblin's own launcher, which is
// the goblin's by the task's folder it runs from. And a run at work in a
// detached tree is the goblin's work.
func TestReadPlacesADetachedRunOnceAProcessOfItsOwnJoinsIt(t *testing.T) {
	// Arrange
	launched := at.Add(-time.Hour)
	later := func(minutes int) time.Time { return launched.Add(time.Duration(minutes) * time.Minute) }
	testCPU := time.Minute
	shells := []Process{
		{PID: 300, ParentPID: 904, Exe: "bash.exe", Created: 30, Started: later(50), Memory: megabyte},
		{PID: 301, ParentPID: 300, Exe: "bash.exe", Created: 31, Started: later(50), Memory: megabyte},
	}
	suite := func() []Process {
		return []Process{
			{PID: 302, ParentPID: 301, Exe: "go.exe", Created: 32, Started: later(61), Memory: 300 * megabyte},
			{PID: 303, ParentPID: 302, Exe: "tree.exe", Created: 33, Started: later(61), Memory: 5 * megabyte},
			{PID: 304, ParentPID: 303, Exe: "branchsync.test.exe", Created: 34, Started: later(61), CPU: testCPU, Memory: 200 * megabyte},
		}
	}
	processes := append(goblinProcesses(launched, 0), shells...)
	commands := map[int]string{
		300: `"C:\Program Files\Git\usr\bin\bash.exe" /c/scratch/tree/tools/whole_suite.sh`,
		301: `"C:\Program Files\Git\usr\bin\bash.exe" /c/scratch/tree/tools/whole_suite.sh`,
		302: `"C:\Program Files\Go\bin\go.exe" test -exec C:\scratch\tree\tools\launcher\tree.exe -p 1 ./internal/branchsync`,
		303: `C:\scratch\tree\tools\launcher\tree.exe C:\Temp\go-build1\b001\branchsync.test.exe -test.timeout=1h`,
		304: `C:\Temp\go-build1\b001\branchsync.test.exe -test.timeout=1h`,
	}
	asked := map[int]int{}
	now := at
	reader := Reader{
		Home:        t.TempDir(),
		Processes:   func() ([]Process, error) { return processes, nil },
		Listeners:   func() (map[int][]int, error) { return nil, nil },
		CommandLine: func(pid int) (string, error) { return commands[pid], nil },
		// Nothing in the suite carries a mark: the launcher is the task's
		// by the task's folder it runs from.
		Owner: func(pid int) string { asked[pid]++; return map[int]string{303: "tree"}[pid] },
		Now:   func() time.Time { return now },
	}
	goblin := Goblin{Meta: state.TaskMeta{ID: "tree", Harness: "codex"}, HarnessPID: 100}

	// Act: the first read finds the suite's two shells alone, and the next
	// two, a minute apart, its go test, the launcher and a test binary using
	// the processor.
	if _, err := reader.Read(context.Background(), goblin); err != nil {
		t.Fatal(err)
	}
	var tree Tree
	for minute := 1; minute <= 2; minute++ {
		now, testCPU = at.Add(time.Duration(minute)*time.Minute), testCPU+30*time.Second
		processes = append(append(goblinProcesses(launched, 0), shells...), suite()...)
		var err error
		if tree, err = reader.Read(context.Background(), goblin); err != nil {
			t.Fatal(err)
		}
	}

	// Assert
	run := child(t, tree, "process:300:30")
	if !run.Detached || run.Group != GroupTest || run.State != Working || run.Memory != 507*megabyte {
		t.Fatalf("the suite = %+v, want the goblin's detached test run at work with its 507 MB", run)
	}
	if names, used := tree.Jobs(); !slices.Contains(names, "bash.exe (pid 300)") || used < 90*time.Second {
		t.Errorf("Jobs() = %v, %s; want the suite named with its 90 s of processor time", names, used)
	}
	if !tree.Working() {
		t.Error("the goblin reads as not working while its suite runs")
	}
	if busy := tree.Busy(); !busy.Equal(at.Add(2 * time.Minute)) {
		t.Errorf("Busy() = %s, want the last read, when the suite used the processor", busy)
	}
	for pid, times := range asked {
		if times != 1 {
			t.Errorf("process %d was asked whose it is %d times over three reads, want once", pid, times)
		}
	}
}

// A machine service a goblin started, such as the no-mistakes daemon every
// gate shares, works for every goblin: its agents using the processor say
// nothing of the work of the goblin that happened to start it.
func TestReadCountsNoMachineServiceAsTheGoblinsWork(t *testing.T) {
	// Arrange
	launched := at.Add(-time.Hour)
	daemonCPU := time.Minute
	processes := func() []Process {
		return append(goblinProcesses(launched, 0),
			Process{PID: 400, ParentPID: 905, Exe: "no-mistakes.exe", Created: 40, Started: launched.Add(50 * time.Minute), Memory: 50 * megabyte},
			Process{PID: 401, ParentPID: 400, Exe: "claude.exe", Created: 41, Started: launched.Add(55 * time.Minute), CPU: daemonCPU, Memory: 400 * megabyte},
		)
	}
	now := at
	reader := Reader{
		Home:      t.TempDir(),
		Processes: func() ([]Process, error) { return processes(), nil },
		Listeners: func() (map[int][]int, error) { return nil, nil },
		CommandLine: func(pid int) (string, error) {
			return map[int]string{400: `no-mistakes.exe daemon`, 401: `claude.exe -p --model opus`}[pid], nil
		},
		Owner: func(pid int) string { return map[int]string{400: "tree"}[pid] },
		Now:   func() time.Time { return now },
	}
	goblin := Goblin{Meta: state.TaskMeta{ID: "tree", Harness: "codex"}, HarnessPID: 100}

	// Act: two reads a minute apart, the gate's agent busy between them.
	if _, err := reader.Read(context.Background(), goblin); err != nil {
		t.Fatal(err)
	}
	now, daemonCPU = at.Add(time.Minute), daemonCPU+30*time.Second
	tree, err := reader.Read(context.Background(), goblin)
	if err != nil {
		t.Fatal(err)
	}

	// Assert
	if daemon := child(t, tree, "process:400:40"); !daemon.Detached {
		t.Errorf("the daemon = %+v, want it shown under the goblin that started it", daemon)
	}
	if names, _ := tree.Jobs(); slices.Contains(names, "no-mistakes.exe (pid 400)") {
		t.Errorf("Jobs() = %v name the daemon, which works for every gate", names)
	}
	// The goblin's own jobs last started at 40 minutes, its browser.
	if tree.Working() || !tree.Busy().Equal(launched.Add(40*time.Minute)) {
		t.Errorf("the goblin reads as working (%t, busy at %s) by the daemon's agent", tree.Working(), tree.Busy())
	}
}

// A goblin's terminal is the host that runs its harness, which has no parent
// once the spawn that started it has returned, and which works in the task's
// folders. It is the terminal, never a job of the goblin's: its processor
// time draws the goblin's screen.
func TestReadKeepsTheGoblinsOwnTerminalOutOfItsJobs(t *testing.T) {
	// Arrange
	launched := at.Add(-time.Hour)
	hostCPU := time.Minute
	processes := func() []Process {
		return []Process{
			{PID: 50, ParentPID: 990, Exe: "cfo.exe", Created: 1, Started: launched.Add(-time.Second), CPU: hostCPU, Memory: 30 * megabyte},
			{PID: 51, ParentPID: 50, Exe: "OpenConsole.exe", Created: 2, Started: launched.Add(-time.Second), CPU: hostCPU, Memory: 10 * megabyte},
			{PID: 100, ParentPID: 50, Exe: "claude.exe", Created: 3, Started: launched, Memory: 500 * megabyte},
		}
	}
	now := at
	reader := Reader{
		Home:        t.TempDir(),
		Processes:   func() ([]Process, error) { return processes(), nil },
		Listeners:   func() (map[int][]int, error) { return nil, nil },
		CommandLine: func(pid int) (string, error) { return map[int]string{50: `cfo.exe host --id tree --dir C:\worktrees\repo\tree`}[pid], nil },
		// The host is the task's by the worktree it is told to work in.
		Owner: func(pid int) string { return map[int]string{50: "tree"}[pid] },
		Now:   func() time.Time { return now },
	}
	goblin := Goblin{Meta: state.TaskMeta{ID: "tree", Harness: "codex"}, HarnessPID: 100}

	// Act: two reads a minute apart, the host drawing the screen between them.
	if _, err := reader.Read(context.Background(), goblin); err != nil {
		t.Fatal(err)
	}
	now, hostCPU = at.Add(time.Minute), hostCPU+30*time.Second
	tree, err := reader.Read(context.Background(), goblin)
	if err != nil {
		t.Fatal(err)
	}

	// Assert
	for _, child := range tree.Children {
		if child.Kind == KindProcess {
			t.Errorf("the goblin's tree holds %+v, want no job: the host is its terminal", child)
		}
	}
	if tree.Working() || tree.Memory != 500*megabyte {
		t.Errorf("the goblin reads as working = %t with %d MB, want idle with its harness's 500 MB", tree.Working(), tree.Memory/megabyte)
	}
}

// A tree that began before the goblin's harness was not started by it,
// whatever its newest processes do: the Overlord's own browser, whose
// renderers come and go, here with one opened on a page in the goblin's
// worktree. None of it is asked whose it is: on 2026-10-11 such trees cost
// a reading every few seconds dozens of reads of other programs' memory.
func TestReadAsksNothingOfATreeOlderThanTheGoblinsHarness(t *testing.T) {
	// Arrange
	launched := at.Add(-time.Hour)
	processes := append(goblinProcesses(launched, 0),
		Process{PID: 700, ParentPID: 990, Exe: "msedge.exe", Created: 70, Started: launched.Add(-24 * time.Hour)},
		Process{PID: 701, ParentPID: 700, Exe: "msedge.exe", Created: 71, Started: at.Add(-time.Minute)},
	)
	asked := map[int]int{}
	reader := Reader{
		Home:        t.TempDir(),
		Processes:   func() ([]Process, error) { return processes, nil },
		Listeners:   func() (map[int][]int, error) { return nil, nil },
		CommandLine: func(pid int) (string, error) { return goblinCommands[pid], nil },
		Owner:       func(pid int) string { asked[pid]++; return map[int]string{701: "tree"}[pid] },
		Now:         func() time.Time { return at },
	}

	// Act
	tree, err := reader.Read(context.Background(), Goblin{Meta: state.TaskMeta{ID: "tree", Harness: "codex"}, HarnessPID: 100})
	if err != nil {
		t.Fatal(err)
	}

	// Assert
	for _, node := range tree.Children {
		if node.Detached {
			t.Errorf("the goblin's tree holds %+v, a tree that began before its harness", node)
		}
	}
	if asked[700] != 0 || asked[701] != 0 {
		t.Errorf("asked whose the older tree's processes are %v, want never", asked)
	}
}

// A run waiting in the heavy line uses no processor while the runs ahead of
// it take their turns, so the line moving is its activity: a goblin waiting
// on it past the busy budget is at work while the turn passes (#965).
func TestAHeavyLineRunWaitingInLineShowsTheLineMovingAsItsActivity(t *testing.T) {
	// Arrange
	launched := at.Add(-3 * time.Hour)
	joined := at.Add(-90 * time.Minute)
	passed := at.Add(-4 * time.Minute)
	processes := append(goblinProcesses(launched, 0),
		Process{PID: 300, ParentPID: 904, Exe: "goblins.exe", Created: 30, Started: joined, Memory: 20 * megabyte},
	)
	commands := maps.Clone(goblinCommands)
	commands[300] = `C:\dev\code-goblins\bin\goblins.exe gate turn --detached -- go test ./internal/...`
	reader := Reader{
		Home:        t.TempDir(),
		Processes:   func() ([]Process, error) { return processes, nil },
		Listeners:   func() (map[int][]int, error) { return map[int][]int{}, nil },
		CommandLine: func(pid int) (string, error) { return commands[pid], nil },
		Owner:       func(pid int) string { return map[int]string{300: "tree"}[pid] },
		HeavyLine: func() ([]verify.Standing, []verify.Standing, error) {
			return []verify.Standing{{Who: "another goblin's suite", PID: 777, Since: passed}}, []verify.Standing{{Who: "go test", PID: 300, Since: joined}}, nil
		},
		Now: func() time.Time { return at },
	}

	// Act
	tree, err := reader.Read(context.Background(), Goblin{Meta: state.TaskMeta{ID: "tree", Harness: "codex"}, HarnessPID: 100})
	if err != nil {
		t.Fatal(err)
	}

	// Assert
	if len(tree.Waits()) == 0 {
		t.Fatalf("Waits() = none, want the run waiting in the heavy line")
	}
	if got := tree.ActivityAt(); !got.Equal(passed) {
		t.Errorf("ActivityAt() = %v, want %v, when the turn last passed", got, passed)
	}
}

// A run a goblin started in the machine's heavy line with cfo gate turn
// --detach waits there, hidden and detached, and tells the goblin how it
// ended, so the goblin waits on it by design while it waits in line or holds
// the turn: on 2026-10-14 a goblin's run waited 50 minutes behind another.
// It is a wait whatever its processor use, and a server left detached is
// none.
func TestReadNamesADetachedHeavyLineRunAsAWait(t *testing.T) {
	// Arrange
	launched := at.Add(-time.Hour)
	later := func(minutes int) time.Time { return launched.Add(time.Duration(minutes) * time.Minute) }
	processes := append(goblinProcesses(launched, 0),
		Process{PID: 300, ParentPID: 904, Exe: "goblins.exe", Created: 30, Started: later(50), Memory: 20 * megabyte},
		Process{PID: 310, ParentPID: 905, Exe: "bash.exe", Created: 31, Started: later(51), Memory: megabyte},
		Process{PID: 311, ParentPID: 310, Exe: "node.exe", Created: 32, Started: later(51), Memory: 99 * megabyte},
	)
	commands := maps.Clone(goblinCommands)
	commands[300] = `C:\dev\code-goblins\bin\goblins.exe gate turn --detached -- go test ./internal/...`
	reader := Reader{
		Home:        t.TempDir(),
		Processes:   func() ([]Process, error) { return processes, nil },
		Listeners:   func() (map[int][]int, error) { return map[int][]int{311: {47391}}, nil },
		CommandLine: func(pid int) (string, error) { return commands[pid], nil },
		Owner:       func(pid int) string { return map[int]string{300: "tree", 311: "tree"}[pid] },
		Now:         func() time.Time { return at },
	}

	// Act
	tree, err := reader.Read(context.Background(), Goblin{Meta: state.TaskMeta{ID: "tree", Harness: "codex"}, HarnessPID: 100})
	if err != nil {
		t.Fatal(err)
	}

	// Assert
	waits := tree.Waits()
	if !slices.ContainsFunc(waits, func(wait string) bool { return strings.HasPrefix(wait, "heavy run ") }) {
		t.Errorf("Waits() = %v, want the run waiting in the heavy line", waits)
	}
	if slices.ContainsFunc(waits, func(wait string) bool { return strings.Contains(wait, "47391") || strings.Contains(wait, "node") }) {
		t.Errorf("Waits() = %v, want no detached server", waits)
	}
}
