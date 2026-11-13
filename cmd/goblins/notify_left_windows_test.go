package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/lifecycle"
	"github.com/fpresta0607/code-goblins/internal/standin"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// A done report sent from a shell with a pipe, read on this machine's own
// processes: a shell in a hidden console of its own, as a goblin's tool call
// runs, running the report and the program it pipes into, beside a server
// the goblin left with a console of its own and a program run beside the
// report that ends before the second reading. The report names the server
// and nothing of the shell it was sent from. This test's process stands in
// for the goblin's harness, read as started an hour ago, so what it starts
// reads as its tool calls. Nothing here shows a window.
func TestADoneReportFromAShellWithAPipeNamesOnlyWhatTheGoblinLeftRunning(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	standin.RemoveAtCleanup(t, dir)
	programs := map[string]string{}
	for _, name := range []string{"report", "piped", "server", "beside"} {
		programs[name] = filepath.Join(dir, name+".exe")
		standin.Put(t, programs[name])
	}
	record := filepath.Join(dir, "started")
	start := func(command *exec.Cmd, flags uint32) *exec.Cmd {
		t.Helper()
		command.Env = append(os.Environ(), standin.Env(record, standin.Rule{Any: true, Hold: true})...)
		if command.SysProcAttr == nil {
			command.SysProcAttr = &syscall.SysProcAttr{}
		}
		command.SysProcAttr.CreationFlags = flags
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = command.Process.Kill(); _ = command.Wait() })
		return command
	}
	server := start(exec.Command(programs["server"]), windows.CREATE_NO_WINDOW)
	shell := exec.Command(filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe"))
	shell.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd.exe /d /s /c ""` + programs["report"] + `" | "` + programs["piped"] + `""`}
	start(shell, windows.CREATE_NO_WINDOW)
	beside := start(exec.Command(programs["beside"]), windows.DETACHED_PROCESS)
	var shellTree []fleettree.Process
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(50 * time.Millisecond) {
		processes, err := fleettree.Processes()
		if err != nil {
			t.Fatal(err)
		}
		shellTree = processesUnder(processes, shell.Process.Pid)
		// Enumeration can see a pipeline child before its console startup
		// finishes. Each stand-in records itself only after entering main.
		started, _ := os.ReadFile(record)
		if strings.Count(string(started), "\n") == 4 && exeIn(shellTree, "report.exe") != nil && exeIn(shellTree, "piped.exe") != nil && exeIn(shellTree, "conhost.exe") != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the shell never ran its pipeline in a console of its own: %+v", shellTree)
		}
	}
	// The shell's programs are this test's to end, each by its pid. One whose
	// id names another program by then has ended.
	t.Cleanup(func() {
		for _, process := range slices.Backward(shellTree) {
			if _, err := lifecycle.Terminate(context.Background(), lifecycle.Identity{PID: process.PID, Started: process.Started}); err != nil && !errors.Is(err, lifecycle.ErrIdentityChanged) {
				t.Errorf("end %s pid %d: %v", process.Exe, process.PID, err)
			}
		}
	})
	report := exeIn(shellTree, "report.exe").PID
	harness := os.Getpid()
	reader := &fleettree.Reader{Processes: func() ([]fleettree.Process, error) {
		processes, err := fleettree.Processes()
		for index := range processes {
			if processes[index].PID == harness {
				processes[index].Started = processes[index].Started.Add(-time.Hour)
			}
		}
		return processes, err
	}}
	var first []fleettree.Placed
	reads := 0
	read := func() ([]fleettree.Placed, error) {
		reads++
		if reads == 2 {
			_ = beside.Process.Kill()
			_ = beside.Wait()
		}
		placed, err := reader.Placed([]fleettree.Goblin{{Meta: state.TaskMeta{ID: "cg-a"}, HarnessPID: harness}})
		if reads == 1 {
			first = placed
		}
		return placed, err
	}
	console := reportConsole(report, harness)

	// Act
	left, err := leftByReport(read, report, console, 0)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	firstPIDs := pidsOf(first)
	for _, pid := range []int{exeIn(shellTree, "conhost.exe").PID, exeIn(shellTree, "piped.exe").PID, beside.Process.Pid, server.Process.Pid} {
		if !slices.Contains(firstPIDs, pid) {
			t.Fatalf("the first reading placed %v, without pid %d, so this run proved nothing", firstPIDs, pid)
		}
	}
	if console == 0 {
		t.Fatal("the report read no console of its own, so this run proved nothing")
	}
	got := pidsOf(left)
	if !slices.Contains(got, server.Process.Pid) {
		t.Errorf("left running = %v, want the server, pid %d", got, server.Process.Pid)
	}
	for _, process := range append(shellTree, fleettree.Process{PID: shell.Process.Pid, Exe: "cmd.exe"}, fleettree.Process{PID: beside.Process.Pid, Exe: "beside.exe"}) {
		if slices.Contains(got, process.PID) {
			t.Errorf("left running = %v, names %s pid %d, which is the reporting shell's or ended before the second reading", got, process.Exe, process.PID)
		}
	}
	// A report that shares its terminal's console reads none of its own.
	if shared := reportConsole(report, exeIn(shellTree, "piped.exe").PID); shared != 0 {
		t.Errorf("a report sharing its terminal's console read console host %d as its own, want none", shared)
	}
}

// processesUnder are the processes that run under pid, each started after
// its parent.
func processesUnder(processes []fleettree.Process, pid int) []fleettree.Process {
	byPID := map[int]fleettree.Process{}
	for _, process := range processes {
		byPID[process.PID] = process
	}
	var under []fleettree.Process
	for queue := []int{pid}; len(queue) > 0; queue = queue[1:] {
		parent, isRunning := byPID[queue[0]]
		if !isRunning {
			continue
		}
		for _, process := range processes {
			if process.ParentPID == parent.PID && process.PID != parent.PID && !process.Started.Before(parent.Started) {
				under = append(under, process)
				queue = append(queue, process.PID)
			}
		}
	}
	return under
}

// exeIn is the process among processes that runs exe, nil when none does.
func exeIn(processes []fleettree.Process, exe string) *fleettree.Process {
	for index := range processes {
		if strings.EqualFold(processes[index].Exe, exe) {
			return &processes[index]
		}
	}
	return nil
}

// A done report runs in the goblin's tool call, whose shell, the console
// host Windows starts beside its hidden console and the programs of its
// pipeline all end with it, so none of them is what the goblin left
// running. On 2026-10-11 four of the first five done reports named such a
// console host or the report's own tail -2, gone a moment later. What the
// goblin did leave, a detached server from an earlier tool call, a
// background shell with its watch, or a server the tool call started with a
// console of its own, is still named. A machine service is not.
func TestADoneReportNamesNothingOfTheToolCallItIsSentFrom(t *testing.T) {
	leftovers := []fleettree.Placed{
		// A server an earlier tool call left, detached from its shell, with
		// the browser it drives.
		detachedAt(70, 69, "node.exe", -30*time.Minute),
		placedAt(71, 70, "chrome.exe", -29*time.Minute),
		// A background shell with its watch.
		byHarnessAt(80, "bash.exe", -20*time.Minute),
		placedAt(81, 80, "node.exe", -20*time.Minute),
	}
	service := detachedAt(90, 89, "no-mistakes.exe", -40*time.Minute)
	service.Service = true
	leftoverConsoles := map[int]int{70: 600, 80: 610, 81: 610, 90: 620}
	tests := []struct {
		name     string
		shell    []fleettree.Placed
		report   int
		console  int
		consoles map[int]int
		alsoLeft []int
	}{
		{
			// goblins notify ... | tail -2, as Elsie sent it: Git Bash's
			// launcher runs the shell, which made the console, runs a copy of
			// itself for the report, and started tail by replacing that
			// copy's Windows process. The report's git call took the id of
			// the server's long gone parent.
			name: "a Git Bash pipeline",
			shell: []fleettree.Placed{
				byHarnessAt(10, "bash.exe", -2*time.Second),
				placedAt(11, 10, "bash.exe", -1990*time.Millisecond),
				placedAt(12, 11, "conhost.exe", -1980*time.Millisecond),
				placedAt(13, 11, "bash.exe", -30*time.Millisecond),
				placedAt(14, 13, "goblins.exe", 0),
				detachedAt(15, 98, "tail.exe", 10*time.Millisecond),
				placedAt(69, 14, "git.exe", 20*time.Millisecond),
			},
			report:   14,
			console:  12,
			consoles: map[int]int{11: 12, 13: 12, 14: 12, 15: 12, 69: 12},
		},
		{
			// A PowerShell tool call, as Toby and Woody sent theirs, which
			// also started a server with a console of its own.
			name: "a PowerShell tool call",
			shell: []fleettree.Placed{
				byHarnessAt(20, "cmd.exe", -time.Second),
				placedAt(21, 20, "conhost.exe", -990*time.Millisecond),
				placedAt(22, 20, "powershell.exe", -900*time.Millisecond),
				placedAt(23, 22, "goblins.exe", 0),
				placedAt(24, 22, "node.exe", -500*time.Millisecond),
				placedAt(25, 24, "conhost.exe", -490*time.Millisecond),
			},
			report:   23,
			console:  21,
			consoles: map[int]int{20: 21, 22: 21, 23: 21, 24: 25},
			alsoLeft: []int{24, 25},
		},
		{
			// timeout 60 goblins notify ..., where Git Bash replaced the
			// shell's copy with timeout, so the report reaches no shell.
			name: "a report under timeout in Git Bash",
			shell: []fleettree.Placed{
				byHarnessAt(30, "bash.exe", -2*time.Second),
				placedAt(31, 30, "bash.exe", -1990*time.Millisecond),
				placedAt(32, 31, "conhost.exe", -1980*time.Millisecond),
				detachedAt(33, 97, "timeout.exe", -30*time.Millisecond),
				placedAt(34, 33, "timeout.exe", -20*time.Millisecond),
				placedAt(35, 34, "goblins.exe", 0),
			},
			report:   35,
			console:  32,
			consoles: map[int]int{31: 32, 33: 32, 34: 32, 35: 32},
		},
		{
			// A harness that runs its tool calls in its terminal's own
			// console, which everything the goblin started there shares, so
			// the console says nothing and only the report's line counts.
			name: "a console the terminal shares",
			shell: []fleettree.Placed{
				byHarnessAt(40, "powershell.exe", -time.Second),
				placedAt(41, 40, "goblins.exe", 0),
				placedAt(42, 41, "git.exe", 10*time.Millisecond),
			},
			report:   41,
			console:  0,
			consoles: map[int]int{40: 630, 41: 630, 42: 630, 70: 630, 80: 630, 81: 630},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			placed := append(append(slices.Clone(test.shell), leftovers...), service)
			consoleOf := func(pid int) (int, error) {
				if console, isKnown := test.consoles[pid]; isKnown {
					return console, nil
				}
				if console, isKnown := leftoverConsoles[pid]; isKnown {
					return console, nil
				}
				return 0, nil
			}

			// Act
			left := leftBeside(placed, test.report, test.console, consoleOf)

			// Assert
			want := append(pidsOf(leftovers), test.alsoLeft...)
			slices.Sort(want)
			if got := pidsOf(left); !slices.Equal(got, want) {
				t.Errorf("left running = %v, want %v", got, want)
			}
		})
	}
}
