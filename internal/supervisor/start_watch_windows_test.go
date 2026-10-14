package supervisor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/install"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// startWatchFleet is a supervisor watching what goblin cg-a starts, beside
// programs of the Overlord's own: its harness at pid 100, a background shell
// at 102, and what each test adds.
type startWatchFleet struct {
	service   *Service
	home      home.Home
	launched  time.Time
	processes []fleettree.Process
	commands  map[int]string
	windows   map[int]bool
	watching  *startWatching
}

func newStartWatchFleet(t *testing.T) *startWatchFleet {
	t.Helper()
	service, h := fleetService(t)
	liveGoblin(t, h, "cg-a", h.Root)
	launched := time.Now().UTC().Add(-time.Hour)
	data, err := json.Marshal(host.Record{ID: "cg-a", HostPID: os.Getpid(), ChildPID: 100, ChildStart: launched, Started: time.Now().UTC(), Pipe: "fixture", Token: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(h.State, "hosts", "cg-a.json"), string(data))
	fleet := &startWatchFleet{
		service:  service,
		home:     h,
		launched: launched,
		processes: []fleettree.Process{
			{PID: 100, ParentPID: 1, Exe: "claude.exe", Created: 1, Started: launched},
			{PID: 102, ParentPID: 100, Exe: "bash.exe", Created: 2, Started: launched.Add(30 * time.Minute)},
			// The Overlord's own browser and terminal, which no goblin started.
			{PID: 500, ParentPID: 4, Exe: "msedge.exe", Created: 50, Started: launched.Add(-24 * time.Hour)},
			{PID: 510, ParentPID: 4, Exe: "WindowsTerminal.exe", Created: 51, Started: launched.Add(-24 * time.Hour)},
		},
		commands: map[int]string{102: `bash -c "eval 'bash tools/run.sh'"`, 500: `"C:\Program Files\Microsoft\Edge\Application\msedge.exe"`},
		windows:  map[int]bool{500: true, 510: true},
		watching: &startWatching{},
	}
	service.Options.Tree = &fleettree.Reader{
		Processes:   func() ([]fleettree.Process, error) { return fleet.processes, nil },
		Listeners:   func() (map[int][]int, error) { return nil, nil },
		CommandLine: func(pid int) (string, error) { return fleet.commands[pid], nil },
	}
	service.Options.Windows = func() (map[int]bool, error) { return fleet.windows, nil }
	service.Options.ConsoleShown = func(int, map[int]bool) (bool, error) { return false, nil }
	// No fixture pid is a real process of the test's: none is ever hidden.
	service.Options.HideConsole = func(int) (bool, error) { return false, nil }
	return fleet
}

// start adds a process the goblin's background shell started.
func (f *startWatchFleet) start(pid int, exe, command string, at time.Time) {
	f.processes = append(f.processes, fleettree.Process{PID: pid, ParentPID: 102, Exe: exe, Created: int64(pid), Started: at})
	f.commands[pid] = command
}

func (f *startWatchFleet) read(t *testing.T, at time.Time) {
	t.Helper()
	if err := f.service.watchStarts(f.watching, at); err != nil {
		t.Fatal(err)
	}
}

// wakes are the check wakes whose detail starts with prefix.
func (f *startWatchFleet) wakes(t *testing.T, prefix string) []wake.Record {
	t.Helper()
	var matching []wake.Record
	for _, record := range fleetWakeRecords(t, f.home, "check") {
		if strings.HasPrefix(record.Detail, prefix) {
			matching = append(matching, record)
		}
	}
	return matching
}

// A window a goblin's process shows on the Overlord's screen wakes the CFO
// once, naming the goblin and what the process runs, and again neither at
// the next reading nor after the supervisor restarts. A program of his own
// is never told, whatever it shows.
func TestAWindowOfAGoblinsProcessWakesTheCFOOnce(t *testing.T) {
	// Arrange
	fleet := newStartWatchFleet(t)
	now := time.Now().UTC()
	fleet.start(103, "msedge.exe", `"C:\Program Files\Microsoft\Edge\Application\msedge.exe" --app=http://localhost:5173`, now.Add(-time.Minute))
	fleet.windows[103] = true

	// Act: two readings, then one by a supervisor that restarted.
	fleet.read(t, now)
	fleet.read(t, now.Add(5*time.Second))
	fleet.watching = &startWatching{}
	fleet.read(t, now.Add(10*time.Second))

	// Assert
	wakes := fleet.wakes(t, "window_shown:")
	if len(wakes) != 1 {
		t.Fatalf("window wakes = %+v, want one", wakes)
	}
	if wakes[0].Key != "cg-a" || !strings.Contains(wakes[0].Detail, "cg-a's msedge.exe (pid 103)") || !strings.Contains(wakes[0].Detail, "--app=http://localhost:5173") {
		t.Errorf("wake = %+v, want cg-a's browser by its pid with its command line", wakes[0])
	}
	for _, record := range fleetWakeRecords(t, fleet.home, "check") {
		if strings.Contains(record.Detail, "pid 500") || strings.Contains(record.Detail, "pid 510") {
			t.Errorf("wake %q names a program of the Overlord's own", record.Detail)
		}
	}
}

// A command a goblin starts again and again wakes the CFO once: here a
// server restarted every 5 seconds for a minute.
func TestALoopOfAGoblinsCommandWakesTheCFOOnce(t *testing.T) {
	// Arrange
	fleet := newStartWatchFleet(t)
	now := time.Now().UTC()

	// Act: a reading every 5 seconds, each finding the server started anew.
	for reading := 0; reading < 13; reading++ {
		at := now.Add(time.Duration(reading) * 5 * time.Second)
		fleet.processes = fleet.processes[:4]
		fleet.start(1000+reading, "node.exe", "node server.js --port 3000", at)
		fleet.read(t, at)
	}

	// Assert
	wakes := fleet.wakes(t, "start_loop:")
	if len(wakes) != 1 {
		t.Fatalf("loop wakes = %+v, want one", wakes)
	}
	if wakes[0].Key != "cg-a" || !strings.Contains(wakes[0].Detail, "cg-a started node.exe 10 times") || !strings.Contains(wakes[0].Detail, "node server.js --port 3000") {
		t.Errorf("wake = %+v, want cg-a's server and its command line", wakes[0])
	}
}

// A Claude Code goblin's own hooks start cfo.exe around every shell call and
// after every other tool call, far more than ten times a minute when it is
// busy. Claude Code starts a hook given as a program and its args itself,
// with no shell, so each is its harness's own child, which is no loop.
// The same command started by the goblin's shell, as a command it runs, is
// one, which shows the readings see each start.
func TestAGoblinsOwnHooksAtEveryToolCallAreNoLoop(t *testing.T) {
	for _, test := range []struct {
		name   string
		parent int
		loops  int
	}{
		{"started by its harness, as Claude Code runs a hook", 100, 0},
		{"started by its shell, as a command it runs", 102, 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			fleet := newStartWatchFleet(t)
			now := time.Now().UTC()
			var commands []string
			for _, hook := range install.GoblinHooks(fleet.home.Root) {
				command := []string{syscall.EscapeArg(hook.Command)}
				for _, arg := range hook.Args {
					command = append(command, syscall.EscapeArg(arg))
				}
				commands = append(commands, strings.Join(command, " "))
			}

			// Act: a reading every 5 seconds for two minutes, each finding
			// both hooks started anew 5 times, as 60 tool calls a minute
			// would.
			pid := 1000
			for reading := 0; reading < 24; reading++ {
				at := now.Add(time.Duration(reading) * 5 * time.Second)
				fleet.processes = fleet.processes[:4]
				for range 5 {
					for _, command := range commands {
						pid++
						fleet.processes = append(fleet.processes, fleettree.Process{PID: pid, ParentPID: test.parent, Exe: "cfo.exe", Created: int64(pid), Started: at})
						fleet.commands[pid] = command
					}
				}
				fleet.read(t, at)
			}

			// Assert
			if wakes := fleet.wakes(t, "start_loop:"); len(wakes) != test.loops {
				t.Fatalf("loop wakes = %+v, want %d", wakes, test.loops)
			}
		})
	}
}

// A goblin that reported done and rests at its prompt with a process it
// started still running wakes the CFO once, a minute into its rest, naming
// the process.
func TestAGoblinRestingWithAProcessLeftRunningWakesTheCFOOnce(t *testing.T) {
	// Arrange
	fleet := newStartWatchFleet(t)
	now := time.Now().UTC()
	fleet.start(103, "node.exe", "node node_modules/vite/bin/vite.js --port 5173", now.Add(-10*time.Minute))
	writeFile(t, filepath.Join(fleet.home.State, "cg-a.status"), now.Add(-2*time.Minute).Format(time.RFC3339)+" done: PR https://github.com/o/r/pull/9\n")
	if err := monitor.WriteObservation(fleet.home.State, monitor.Observation{TaskID: "cg-a", Endpoint: (herdr.Target{}).String(), EndpointVerdict: monitor.ProbePresent, Digest: "at its prompt", LastObserved: now, LastSeen: now, LastProgress: now.Add(-2 * time.Minute), Health: monitor.HealthIdle, Reason: monitor.None}); err != nil {
		t.Fatal(err)
	}

	// Act: readings over two minutes of its rest.
	for offset := time.Duration(0); offset <= 2*time.Minute; offset += 30 * time.Second {
		fleet.read(t, now.Add(offset))
	}

	// Assert
	wakes := fleet.wakes(t, "left_running:")
	if len(wakes) != 1 {
		t.Fatalf("leftover wakes = %+v, want one", wakes)
	}
	if !strings.Contains(wakes[0].Detail, "cg-a reported done") || !strings.Contains(wakes[0].Detail, `pid 102 bash.exe "bash -c eval 'bash tools/run.sh'" (under it node.exe pid 103)`) {
		t.Errorf("wake = %q, want the shell cg-a left holding its dev server", wakes[0].Detail)
	}
}

// A console window a goblin's careless start opened is hidden at the
// reading that sees it, once, and the CFO is woken once naming the task and
// the command line. A console shared by a program and what it runs is
// hidden through the first of them, which the wake names. A program of the
// Overlord's own is never touched.
func TestACarelessConsoleOfAGoblinsProcessIsHiddenAndToldOnce(t *testing.T) {
	// Arrange
	fleet := newStartWatchFleet(t)
	now := time.Now().UTC()
	// The child is listed first, as a reading may list it.
	fleet.processes = append(fleet.processes, fleettree.Process{PID: 104, ParentPID: 103, Exe: "goblins.exe", Created: 104, Started: now.Add(-time.Minute)})
	fleet.commands[104] = `goblins.exe gate turn -- go test ./...`
	fleet.start(103, "cmd.exe", `cmd /c goblins.exe gate turn -- go test ./...`, now.Add(-2*time.Minute))
	fleet.service.Options.ConsoleShown = func(pid int, _ map[int]bool) (bool, error) { return pid == 103 || pid == 104 || pid == 510, nil }
	var hidden []int
	fleet.service.Options.HideConsole = func(pid int) (bool, error) {
		hidden = append(hidden, pid)
		// The two share one console, which the first hide hides.
		return len(hidden) == 1, nil
	}

	// Act
	fleet.read(t, now)
	fleet.read(t, now.Add(5*time.Second))

	// Assert
	if len(hidden) != 2 || hidden[0] != 103 || hidden[1] != 104 {
		t.Fatalf("hidden = %v, want 103 then 104, each once", hidden)
	}
	wakes := fleet.wakes(t, "window_shown:")
	if len(wakes) != 1 {
		t.Fatalf("window wakes = %+v, want one", wakes)
	}
	if wakes[0].Key != "cg-a" || !strings.Contains(wakes[0].Detail, "cg-a's cmd.exe (pid 103)") || !strings.Contains(wakes[0].Detail, "which the supervisor hid") || !strings.Contains(wakes[0].Detail, "gate turn -- go test ./...") {
		t.Errorf("wake = %+v, want cg-a's cmd.exe hidden, with its command line", wakes[0])
	}
}
