package reap

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/standin"
)

// A tool call's shell starts a program and ends while it runs: the listing
// the sweep takes reads the program with its parent gone, the handles it
// holds and the processor time it has used, the sweep lists it for the
// terminal that owns it, and naming its pid ends it.
func TestAnOrphanedChildOfAToolCallIsListedBySweepAndEndedByPid(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	standin.RemoveAtCleanup(t, dir)
	program := filepath.Join(dir, "search.exe")
	standin.Put(t, program)
	environment := append(os.Environ(), standin.Env("", standin.Rule{Any: true, Hold: true})...)
	shell := exec.Command(program, "shell")
	shell.Env = environment
	if err := shell.Start(); err != nil {
		t.Fatal(err)
	}
	shellHandle := standin.Hold(t, shell.Process.Pid)
	parent, err := windows.OpenProcess(windows.PROCESS_CREATE_PROCESS|windows.PROCESS_DUP_HANDLE, false, uint32(shell.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(parent)
	search := exec.Command(program, "search")
	search.Env = environment
	search.SysProcAttr = &syscall.SysProcAttr{ParentProcess: syscall.Handle(parent)}
	if err := search.Start(); err != nil {
		t.Fatal(err)
	}
	searchHandle := standin.Hold(t, search.Process.Pid)
	if err := windows.TerminateProcess(shellHandle, 1); err != nil {
		t.Fatal(err)
	}
	if event, err := windows.WaitForSingleObject(shellHandle, 60000); err != nil || event != windows.WAIT_OBJECT_0 {
		t.Fatalf("the shell had not ended 60 s after it was ended: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	listed, err := CIMProcesses{Commands: execx.OSRunner{}}.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var orphan Process
	for _, row := range listed {
		if row.PID == search.Process.Pid {
			orphan = row
		}
	}
	if orphan.PID == 0 || orphan.ParentPID != shell.Process.Pid || orphan.Handles <= 0 || orphan.Start.IsZero() {
		t.Fatalf("the listing read the search as %+v, want pid %d under the ended shell %d with its handles and start", orphan, search.Process.Pid, shell.Process.Pid)
	}
	// What the janitor's second reading says of a process a running
	// terminal owns by its mark, its task's folders or its parents.
	orphan.Owner = "task-1"
	runner := &gitRunner{}
	service := newService(t, testHome(t), Inventory{At: time.Now().UTC(), Processes: []Process{orphan}}, runner)
	service.Kill = func(_ context.Context, pid int) error {
		runner.killed = append(runner.killed, pid)
		process, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
		if err != nil {
			return err
		}
		defer windows.CloseHandle(process)
		return windows.TerminateProcess(process, 1)
	}

	// Act
	audit, auditErr := service.Audit(ctx, Options{})
	applied, applyErr := service.Apply(ctx, Options{Force: map[string]bool{strconv.Itoa(orphan.PID): true}})

	// Assert
	if auditErr != nil || applyErr != nil {
		t.Fatalf("audit: %v; apply: %v", auditErr, applyErr)
	}
	if finding := onlyFinding(t, audit, DetachedProcess); finding.PID != orphan.PID || finding.Handles != orphan.Handles || !finding.Held() {
		t.Fatalf("the sweep listed %+v, want pid %d held with its %d handles", finding, orphan.PID, orphan.Handles)
	}
	if len(runner.killed) != 1 || runner.killed[0] != orphan.PID || len(applied.Applied) != 1 {
		t.Fatalf("naming pid %d ended %v and applied %v, want that pid alone", orphan.PID, runner.killed, applied.Applied)
	}
	if event, err := windows.WaitForSingleObject(searchHandle, 60000); err != nil || event != windows.WAIT_OBJECT_0 {
		t.Fatalf("the search still ran 60 s after its pid was named: %v", err)
	}
}
