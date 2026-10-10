package janitor

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/lifecycle"
)

// planned is what a plan decided, by pid, for a test to compare.
type planned struct {
	Ending, Left, Watching []int
}

func pidsOf(items []ProcessItem) []int {
	var pids []int
	for _, item := range items {
		pids = append(pids, item.PID)
	}
	return pids
}

func planOf(processes []Process, owners []Owner, watched []Watched, now time.Time) planned {
	ending, left, watching, _ := planProcesses(processes, owners, watched, now)
	var kept []int
	for _, watch := range watching {
		kept = append(kept, watch.PID)
	}
	return planned{Ending: pidsOf(ending), Left: pidsOf(left), Watching: kept}
}

// goneTerminal is terminal gb-task with no host left to run it, and
// goneTerminalsServer the one process it left running.
func goneTerminal() Owner {
	return Owner{ID: "gb-task", Marks: []lifecycle.Mark{{Terminal: "gb-task", ProofSum: "own-digest"}}}
}

func goneTerminalsServer(started time.Time) []Process {
	return []Process{{Process: lifecycle.Process{PID: 10, ParentPID: 900, Name: "node.exe", Started: started, Mark: lifecycle.Mark{Terminal: "gb-task", ProofSum: "own-digest"}}}}
}

// On 2026-10-09 two browser bridges whose parents were gone held 3.7 GB, and
// shells left by commands that had long returned sat for 41 minutes, while
// five goblins were paused for memory. Nothing looked for what a terminal
// left behind. The sweep ends what is a terminal's own once the terminal is
// gone, by the same evidence its teardown reads, and never what is the
// Overlord's or the machine's.
func TestTheSweepEndsWhatAGoneTerminalLeftAndNeverWhatIsNotItsOwn(t *testing.T) {
	// Arrange
	scratch := filepath.Join(t.TempDir(), "scratch", "gb-task")
	elsewhere := t.TempDir()
	started := time.Date(2026, 10, 9, 14, 20, 0, 0, time.UTC)
	at := func(seconds int) time.Time { return started.Add(time.Duration(seconds) * time.Second) }
	mark := lifecycle.Mark{Terminal: "gb-task", ProofSum: "own-digest"}
	process := func(pid, parent int, name string, start time.Time, from lifecycle.Process) Process {
		from.PID, from.ParentPID, from.Name, from.Started = pid, parent, name, start
		return Process{Process: from, Memory: 100 << 20}
	}
	processes := []Process{
		// A browser bridge and the browser under it, started from Git Bash.
		process(10, 900, "node.exe", at(0), lifecycle.Process{Directory: elsewhere, Arguments: []string{"node", `C:\npm\chrome-devtools-axi\dist\bin\chrome-devtools-axi-bridge.js`}, Mark: mark}),
		process(11, 10, "chrome.exe", at(1), lifecycle.Process{Arguments: []string{"chrome.exe", "--headless", "--remote-debugging-pipe"}, Mark: mark}),
		// A shell left in the task's scratch folder, which carries no mark.
		process(12, 901, "bash.exe", at(2), lifecycle.Process{Directory: scratch}),
		// The Overlord's browser, which a sign-in the goblin ran opened.
		process(20, 902, "chrome.exe", at(3), lifecycle.Process{Arguments: []string{`C:\Program Files\Google\Chrome\Application\chrome.exe`}, Mark: mark, HasWindow: true}),
		process(21, 20, "chrome.exe", at(4), lifecycle.Process{Arguments: []string{"chrome.exe", "--type=renderer"}, Mark: mark}),
		// The Scrawl server the goblin's command started for every goblin.
		process(30, 903, "node.exe", at(5), lifecycle.Process{Directory: scratch, Arguments: []string{"node", `C:\npm\node_modules\lavish-axi\dist\server.mjs`, "server", "--port", "4455"}, Mark: mark}),
		// A gate's agent the daemon started with the goblin's environment.
		process(40, 904, "no-mistakes.exe", at(6), lifecycle.Process{Arguments: []string{"no-mistakes", "daemon", "run"}, Mark: mark}),
		process(41, 40, "codex.exe", at(7), lifecycle.Process{Directory: elsewhere, Mark: mark, IsGateAgent: true}),
		// The CFO reading the paused goblin's worktree from its own terminal.
		process(50, 0, "bash.exe", at(8), lifecycle.Process{Directory: elsewhere, Mark: lifecycle.Mark{Terminal: "cfo", ProofSum: "cfo-digest"}}),
		process(51, 50, "git.exe", at(9), lifecycle.Process{Directory: scratch}),
		// A program that is nobody's the sweep knows.
		process(60, 905, "node.exe", at(10), lifecycle.Process{Directory: elsewhere}),
	}
	owners := []Owner{
		{ID: "gb-task", Marks: []lifecycle.Mark{mark}, Directories: []string{scratch}},
		{ID: "cfo", Marks: []lifecycle.Mark{{Terminal: "cfo", ProofSum: "cfo-digest"}}, HostPID: 50},
	}

	// Act
	got := planOf(processes, owners, nil, at(3600))

	// Assert
	want := planned{Ending: []int{10, 11, 12}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("plan = %+v, want %+v: the bridge, its browser and the shell end, and nothing else is touched or named", got, want)
	}
}

// A detached tree of a running terminal whose goblin has delivered and rests
// is ended only once it has done nothing for an hour, and the count starts
// again whenever it works. What a goblin that works left is never ended this
// way (TestNothingOfAWorkingGoblinsIsIdleHoweverLongItSatStill).
func TestTheSweepEndsADetachedTreeOfARunningTerminalOnlyOnceItSatIdle(t *testing.T) {
	started := time.Date(2026, 10, 9, 14, 20, 0, 0, time.UTC)
	mark := lifecycle.Mark{Terminal: "gb-task", ProofSum: "own-digest"}
	owners := []Owner{{ID: "gb-task", Marks: []lifecycle.Mark{mark}, HostPID: 1, AtRestSince: started}}
	// seen is the tree as an earlier sweep recorded it: its server had used
	// two seconds of processor time and its worker one.
	seen := func(serverStarted, since time.Time) []Watched {
		return []Watched{{PID: 10, Started: serverStarted, Since: since, Members: []WatchedMember{
			{PID: 10, Started: serverStarted, CPU: 2 * time.Second},
			{PID: 11, Started: started.Add(4 * time.Second), CPU: time.Second},
		}}}
	}
	tree := func(serverCPU, workerCPU time.Duration) []Process {
		return []Process{
			{Process: lifecycle.Process{PID: 1, Name: "cfo.exe", Started: started}},
			{Process: lifecycle.Process{PID: 2, ParentPID: 1, Name: "claude.exe", Started: started.Add(time.Second), Mark: mark}},
			// What the harness started and still holds is never detached.
			{Process: lifecycle.Process{PID: 3, ParentPID: 2, Name: "node.exe", Started: started.Add(2 * time.Second), Mark: mark}},
			// A server left in the background, whose shell has exited.
			{Process: lifecycle.Process{PID: 10, ParentPID: 900, Name: "node.exe", Started: started.Add(3 * time.Second), Mark: mark}, CPU: serverCPU},
			{Process: lifecycle.Process{PID: 11, ParentPID: 10, Name: "node.exe", Started: started.Add(4 * time.Second), Mark: mark}, CPU: workerCPU},
		}
	}
	firstSeen := started.Add(time.Hour)
	for _, test := range []struct {
		name    string
		tree    []Process
		watched []Watched
		now     time.Time
		want    planned
		since   time.Time
	}{
		{name: "first seen", tree: tree(2*time.Second, time.Second), now: firstSeen, want: planned{Watching: []int{10}}, since: firstSeen},
		{name: "idle for less than the hour", tree: tree(2*time.Second, time.Second), watched: seen(started.Add(3*time.Second), firstSeen), now: firstSeen.Add(59 * time.Minute), want: planned{Watching: []int{10}}, since: firstSeen},
		{name: "idle for the hour", tree: tree(2*time.Second, time.Second), watched: seen(started.Add(3*time.Second), firstSeen), now: firstSeen.Add(time.Hour), want: planned{Ending: []int{10, 11}}},
		{name: "it worked since", tree: tree(2*time.Second, 4*time.Second), watched: seen(started.Add(3*time.Second), firstSeen), now: firstSeen.Add(time.Hour), want: planned{Watching: []int{10}}, since: firstSeen.Add(time.Hour)},
		{name: "another process took its pid", tree: tree(2*time.Second, time.Second), watched: seen(started.Add(-time.Hour), firstSeen), now: firstSeen.Add(time.Hour), want: planned{Watching: []int{10}}, since: firstSeen.Add(time.Hour)},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			ending, left, watching, _ := planProcesses(test.tree, owners, test.watched, test.now)

			// Assert
			var kept []int
			for _, watch := range watching {
				kept = append(kept, watch.PID)
			}
			if got := (planned{Ending: pidsOf(ending), Left: pidsOf(left), Watching: kept}); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("plan = %+v, want %+v", got, test.want)
			}
			if len(watching) == 1 && !watching[0].Since.Equal(test.since) {
				t.Fatalf("the tree is counted idle since %s, want %s", watching[0].Since, test.since)
			}
		})
	}
}

// A browser bridge keeps its page drawing whether or not anything drives it,
// so its processor time never says it is idle: on 2026-10-09 three bridges
// hours old used most of a processor between them. The bridge of a running
// terminal whose goblin has delivered and rests is ended once its session
// sat unused for an hour, by the time the tool last wrote its session's
// files. A working goblin's bridge is never ended this way.
func TestTheSweepEndsARunningTerminalsBridgeOnceItsSessionSatUnused(t *testing.T) {
	started := time.Date(2026, 10, 9, 14, 20, 0, 0, time.UTC)
	now := started.Add(3 * time.Hour)
	mark := lifecycle.Mark{Terminal: "gb-task", ProofSum: "own-digest"}
	owners := []Owner{{ID: "gb-task", Marks: []lifecycle.Mark{mark}, HostPID: 1, AtRestSince: started}}
	bridge := []string{"node", `C:\npm\chrome-devtools-axi\dist\bin\chrome-devtools-axi-bridge.js`}
	for _, test := range []struct {
		name     string
		lastUsed time.Time
		want     planned
	}{
		{name: "unused for two hours while its page draws", lastUsed: now.Add(-2 * time.Hour), want: planned{Ending: []int{10, 11}}},
		{name: "used ten minutes ago", lastUsed: now.Add(-10 * time.Minute), want: planned{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			processes := []Process{
				{Process: lifecycle.Process{PID: 1, Name: "cfo.exe", Started: started}},
				{Process: lifecycle.Process{PID: 10, ParentPID: 900, Name: "node.exe", Started: started.Add(time.Minute), Arguments: bridge, Mark: mark}, CPU: 40 * time.Minute, LastUsed: test.lastUsed},
				{Process: lifecycle.Process{PID: 11, ParentPID: 10, Name: "chrome.exe", Started: started.Add(2 * time.Minute), Arguments: []string{"chrome.exe", "--headless"}, Mark: mark}, CPU: 50 * time.Minute},
			}
			// The last sweep saw it with far less processor time used.
			watched := []Watched{{PID: 10, Started: started.Add(time.Minute), Since: now.Add(-time.Hour), Members: []WatchedMember{{PID: 10, Started: started.Add(time.Minute), CPU: time.Minute}}}}

			// Act
			got := planOf(processes, owners, watched, now)

			// Assert
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("plan = %+v, want %+v", got, test.want)
			}
		})
	}
}

// A pause, a resume or a stop ends what it means to end itself. While one is
// under way the sweep leaves that terminal's processes to it.
func TestTheSweepLeavesATerminalThatIsChangingToItsOwnTeardown(t *testing.T) {
	// Arrange
	started := time.Date(2026, 10, 9, 14, 20, 0, 0, time.UTC)
	mark := lifecycle.Mark{Terminal: "gb-task", ProofSum: "own-digest"}
	processes := []Process{{Process: lifecycle.Process{PID: 10, ParentPID: 900, Name: "node.exe", Started: started, Mark: mark}}}
	owners := []Owner{{ID: "gb-task", Marks: []lifecycle.Mark{mark}, IsChanging: true}}

	// Act
	got := planOf(processes, owners, nil, started.Add(time.Hour))

	// Assert
	if !reflect.DeepEqual(got, planned{}) {
		t.Fatalf("plan = %+v, want nothing ended, named or watched", got)
	}
}

// What nothing proves the fleet's own is never ended. The CFO is told of it
// once, with what it costs: a browser bridge with no mark, as the one that
// held 2.5 GB on 2026-10-09 under the tool's unnamed session, and a gate
// agent's process left behind by a gate that is gone.
func TestTheSweepNamesWhatItCannotProveAndEndsNoneOfIt(t *testing.T) {
	// Arrange
	started := time.Date(2026, 10, 9, 14, 20, 0, 0, time.UTC)
	at := func(seconds int) time.Time { return started.Add(time.Duration(seconds) * time.Second) }
	bridge := []string{"node", `C:\npm\chrome-devtools-axi\dist\bin\chrome-devtools-axi-bridge.js`}
	processes := []Process{
		{Process: lifecycle.Process{PID: 10, ParentPID: 900, Name: "node.exe", Started: at(0), Arguments: bridge}, Memory: 200 << 20},
		{Process: lifecycle.Process{PID: 11, ParentPID: 10, Name: "chrome.exe", Started: at(1), Arguments: []string{"chrome.exe", "--headless"}}, Memory: 2300 << 20},
		// A bridge the command that started it still waits on.
		{Process: lifecycle.Process{PID: 20, Name: "cmd.exe", Started: at(2)}},
		{Process: lifecycle.Process{PID: 21, ParentPID: 20, Name: "node.exe", Started: at(3), Arguments: bridge}},
		// A gate agent's leftover, and an agent of a gate that still runs.
		{Process: lifecycle.Process{PID: 30, ParentPID: 901, Name: "node.exe", Started: at(4), IsGateAgent: true}, Memory: 50 << 20},
		{Process: lifecycle.Process{PID: 40, Name: "no-mistakes.exe", Started: at(5), Arguments: []string{"no-mistakes", "daemon", "run"}}},
		{Process: lifecycle.Process{PID: 41, ParentPID: 40, Name: "codex.exe", Started: at(6), IsGateAgent: true}},
	}

	// Act
	ending, left, watching, _ := planProcesses(processes, nil, nil, at(3600))

	// Assert
	if len(ending) != 0 || len(watching) != 0 {
		t.Fatalf("the sweep means to end %v and watch %v, want neither", pidsOf(ending), watching)
	}
	if got := pidsOf(left); !reflect.DeepEqual(got, []int{10, 30}) {
		t.Fatalf("left for the CFO = %v, want the parentless bridge and the gate agent's leftover", got)
	}
	if left[0].Memory != 2500<<20 || !strings.Contains(left[0].Command, "chrome-devtools-axi-bridge.js") {
		t.Fatalf("the bridge is named as %+v, want its command and the memory of its whole tree", left[0])
	}
}

// A command line can hold a credential, and what the sweep names is kept in
// its record and shown in a wake.
func TestAProcessIsNamedWithoutWhatItsCommandLineHides(t *testing.T) {
	for _, test := range []struct {
		arguments []string
		want      string
	}{
		{[]string{"node", "server.js", "--port", "4455"}, "node server.js --port 4455"},
		{[]string{"curl", "--token=abc123", "https://example.test/hook?signature=def456"}, "curl --token=<hidden> https://example.test/hook"},
		{[]string{"tool", "--api-key", "abc123", "run"}, "tool --api-key <hidden> run"},
		{[]string{"tool", "PASSWORD=hunter2"}, "tool PASSWORD=<hidden>"},
		{[]string{"node", strings.Repeat("x", 200)}, "node " + strings.Repeat("x", 152) + "..."},
	} {
		if got := commandOf(test.arguments); got != test.want {
			t.Errorf("commandOf(%q) = %q, want %q", test.arguments, got, test.want)
		}
	}
}
