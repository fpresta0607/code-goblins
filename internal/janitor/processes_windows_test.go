package janitor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/standin"
)

// The fixtures below run as separate processes: a terminal's host, the
// program in it, and what that program leaves behind, each reporting the
// processes it started to the file CFO_JANITOR_PIDS names, one line each.
const (
	janitorHostState = "CFO_JANITOR_HOST_STATE"
	janitorPIDs      = "CFO_JANITOR_PIDS"
	janitorAway      = "CFO_JANITOR_AWAY"
	janitorChrome    = "CFO_JANITOR_CHROME"
)

// TestJanitorHostFixture hosts the terminal fixture, as cfo host runs a
// goblin's terminal.
func TestJanitorHostFixture(t *testing.T) {
	stateDir := os.Getenv(janitorHostState)
	if stateDir == "" {
		return
	}
	_ = host.Run(stateDir, host.Spec{ID: "g1", Args: []string{os.Args[0], "-test.run=^TestJanitorTerminalFixture$"}, Dir: os.Getenv(janitorAway), Cols: 80, Rows: 25})
}

// TestJanitorTerminalFixture is a goblin that leaves two processes behind,
// each outside its terminal's job and in a folder that is no task's: a
// server of its own, and the Overlord's browser, which a sign-in it ran
// opened. Both carry the terminal's mark, as everything started in it does.
func TestJanitorTerminalFixture(t *testing.T) {
	pids := os.Getenv(janitorPIDs)
	if pids == "" || os.Getenv(janitorHostState) == "" {
		return
	}
	leave := func(program string) int {
		left := exec.Command(program, "-test.run=^TestJanitorLeftFixture$")
		left.Dir = os.Getenv(janitorAway)
		left.Env = append(os.Environ(), "CFO_JANITOR_LEFT=1")
		left.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_BREAKAWAY_FROM_JOB}
		if err := left.Start(); err != nil {
			t.Fatal(err)
		}
		return left.Process.Pid
	}
	lines := "server " + strconv.Itoa(leave(os.Args[0])) + "\nchrome " + strconv.Itoa(leave(os.Getenv(janitorChrome))) + "\n"
	if err := os.WriteFile(pids, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Minute)
}

// TestJanitorLeftFixture is a process that waits to be ended.
func TestJanitorLeftFixture(t *testing.T) {
	if os.Getenv("CFO_JANITOR_LEFT") == "1" {
		time.Sleep(time.Minute)
	}
}

// The sweep, run on the real machine for a home of the test's own, ends what
// a terminal that is gone left running and leaves the Overlord's browser,
// though a goblin opened it and it carries the goblin's mark. The server
// stands in for the browser bridges of 2026-10-09: outside its terminal's
// job, at work in no folder of a task's, its parent gone. Only the test's
// home has a terminal, so nothing of the machine's own fleet is judged.
func TestTheSweepEndsAGoneTerminalsStandInAndLeavesAStandInForHisChrome(t *testing.T) {
	// Arrange
	root := t.TempDir()
	h := home.Home{Root: root, State: filepath.Join(root, "state")}
	away := t.TempDir()
	standin.RemoveAtCleanup(t, away)
	programs := t.TempDir()
	standin.RemoveAtCleanup(t, programs)
	binary, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	chrome := filepath.Join(programs, "chrome.exe")
	if err := os.WriteFile(chrome, binary, 0o755); err != nil {
		t.Fatal(err)
	}
	// Windows scans a program the first time it starts, which a loaded
	// machine can take seconds over, so the stand-in starts once first.
	if output, err := exec.Command(chrome, "-test.run=^$").CombinedOutput(); err != nil {
		t.Fatalf("the stand-in for Chrome did not run: %v\n%s", err, output)
	}
	pids := filepath.Join(t.TempDir(), "pids")
	terminal := exec.Command(os.Args[0], "-test.run=^TestJanitorHostFixture$")
	terminal.Env = append(os.Environ(), janitorHostState+"="+h.State, janitorPIDs+"="+pids, janitorAway+"="+away, janitorChrome+"="+chrome)
	terminal.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP, HideWindow: true}
	if err := terminal.Start(); err != nil {
		t.Fatal(err)
	}
	ended := make(chan struct{})
	go func() { _ = terminal.Wait(); close(ended) }()
	t.Cleanup(func() {
		_ = terminal.Process.Kill()
		<-ended
	})
	started := map[string]int{}
	for deadline := time.Now().Add(30 * time.Second); len(started) < 2; time.Sleep(50 * time.Millisecond) {
		if data, err := os.ReadFile(pids); err == nil {
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				if name, value, ok := strings.Cut(line, " "); ok {
					started[name], _ = strconv.Atoi(value)
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the terminal fixture reported %v, want its server and the browser", started)
		}
	}
	held := map[string]windows.Handle{}
	for name, pid := range started {
		held[name] = standin.Hold(t, pid)
	}
	isRunning := func(name string) bool {
		event, _ := windows.WaitForSingleObject(held[name], 2000)
		return event == uint32(windows.WAIT_TIMEOUT)
	}
	cfg := Config{
		Home:       h,
		Now:        time.Now(),
		Processes:  ReadProcesses,
		Owners:     func() ([]Owner, []string) { return ReadOwners(h) },
		EndProcess: EndProcess,
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()

	// listed says what of the terminal and what it left still runs, with the
	// private memory of each, for the test's log to show before and after.
	terminalPID := terminal.Process.Pid
	listed := func(when string) {
		running, err := fleettree.Processes()
		if err != nil {
			t.Fatal(err)
		}
		lines := []string{when + ":"}
		var total uint64
		for _, entry := range []struct {
			name string
			pid  int
		}{{"terminal host", terminalPID}, {"server it left", started["server"]}, {"stand-in for his Chrome", started["chrome"]}} {
			state := "ended"
			if index := slices.IndexFunc(running, func(process fleettree.Process) bool { return process.PID == entry.pid }); index >= 0 {
				state = fmt.Sprintf("running, %.1f MB", float64(running[index].Memory)/(1<<20))
				total += running[index].Memory
			}
			lines = append(lines, fmt.Sprintf("  %-24s pid %-6d %s", entry.name, entry.pid, state))
		}
		lines = append(lines, fmt.Sprintf("  %-24s %.1f MB", "in all", float64(total)/(1<<20)))
		t.Log(strings.Join(lines, "\n"))
	}
	listed("before, with the terminal running")

	// A terminal that runs keeps what it started.
	var whileRunning Record
	cfg.sweepProcesses(ctx, &whileRunning)
	if len(whileRunning.Processes.Ended) != 0 || !isRunning("server") {
		t.Fatalf("with its terminal running the sweep ended %+v, want nothing", whileRunning.Processes.Ended)
	}

	// Act: the terminal's host ends, as a failed goblin's does, and the next
	// sweep runs.
	if err := terminal.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	<-ended
	var record Record
	cfg.sweepProcesses(ctx, &record)

	// Assert
	stillRuns := map[string]bool{"server": isRunning("server"), "chrome": isRunning("chrome")}
	listed("after the terminal ended and the sweep ran")
	if stillRuns["server"] {
		t.Errorf("the gone terminal's server, pid %d, still runs after the sweep: ended %+v, notes %v", started["server"], record.Processes.Ended, record.Notes)
	}
	if !stillRuns["chrome"] {
		t.Errorf("the stand-in for his Chrome, pid %d, was ended by the sweep: ended %+v", started["chrome"], record.Processes.Ended)
	}
	if len(record.Processes.Ended) != 1 || record.Processes.Ended[0].PID != started["server"] || record.Processes.Ended[0].Owner != "g1" {
		t.Errorf("the sweep recorded ending %+v, want the server alone, as terminal g1's", record.Processes.Ended)
	}
}

// A sweep that cannot read the machine ends nothing and keeps what the last
// sweep was watching, so a tree's idle time is not lost to one bad reading.
// One it cannot end is a note, and no entry among the ended.
func TestTheSweepEndsNothingItCouldNotReadOrEnd(t *testing.T) {
	// Arrange
	started := time.Date(2026, 10, 9, 14, 20, 0, 0, time.UTC)
	watched := []Watched{{PID: 10, Started: started, CPU: time.Second, Since: started}}
	unreadable := Config{Now: started.Add(time.Hour), Watched: watched,
		Processes: func(context.Context) ([]Process, error) { return nil, errors.New("access denied") },
		Owners:    func() ([]Owner, []string) { return nil, nil },
		EndProcess: func(context.Context, ProcessItem) error {
			t.Error("a process was ended on a reading that failed")
			return nil
		},
	}
	refused := unreadable
	refused.Processes = func(context.Context) ([]Process, error) { return goneTerminalsServer(started), nil }
	refused.Owners = func() ([]Owner, []string) { return []Owner{goneTerminal()}, []string{"a note of the owners' own"} }
	refused.EndProcess = func(context.Context, ProcessItem) error { return errors.New("it would not end") }

	// Act
	var unread, unended Record
	unreadable.sweepProcesses(t.Context(), &unread)
	refused.sweepProcesses(t.Context(), &unended)

	// Assert
	if len(unread.Processes.Ended) != 0 || len(unread.Processes.Watched) != 1 || len(unread.Notes) != 1 {
		t.Errorf("an unreadable machine: %+v with notes %v, want nothing ended, the watch kept and one note", unread.Processes, unread.Notes)
	}
	if len(unended.Processes.Ended) != 0 || len(unended.Notes) != 2 || !strings.Contains(unended.Notes[1], "pid 10 could not be ended") {
		t.Errorf("a process that would not end: %+v with notes %v, want nothing recorded as ended and the refusal noted after the owners' note", unended.Processes, unended.Notes)
	}
}
