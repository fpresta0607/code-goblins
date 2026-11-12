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
