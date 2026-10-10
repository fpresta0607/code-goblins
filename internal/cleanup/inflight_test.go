package cleanup

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// lockHolder makes the test binary another command of the fleet's: one that
// holds, or tries to take, one of a task's locks from a process of its own,
// as cfo resume, cfo switch and cfo pause do. TestMain runs it.
const lockHolder = "hold-a-task-lock"

// runLockHolder takes the lock arguments name in the state directory they
// name. Told to hold, it says "held" and keeps the lock until its standard
// input closes. Told to try, it says whether it got the lock and ends.
func runLockHolder(arguments []string) {
	mode, stateDir, name := arguments[0], arguments[1], arguments[2]
	if _, err := lock.AcquireExclusiveNamed(stateDir, name); err != nil {
		fmt.Println("refused: " + err.Error())
		return
	}
	fmt.Println("held")
	if mode == "hold" {
		_, _ = io.Copy(io.Discard, os.Stdin)
	}
	if err := lock.ReleaseExclusiveNamed(stateDir, name); err != nil {
		fmt.Println("release: " + err.Error())
	}
}

// heldByAnotherCommand holds a task's lock from a process of its own until
// the test ends.
func heldByAnotherCommand(t *testing.T, stateDir, name string) {
	t.Helper()
	holder := exec.Command(os.Args[0], lockHolder, "hold", stateDir, name)
	stdin, err := holder.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := holder.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = holder.Wait()
	})
	if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || strings.TrimSpace(line) != "held" {
		t.Fatalf("the other command did not take %s: %q, %v", name, line, err)
	}
}

// triedByAnotherCommand is what another command prints when it tries to take
// a task's lock from a process of its own.
func triedByAnotherCommand(t *testing.T, stateDir, name string) string {
	t.Helper()
	output, err := exec.Command(os.Args[0], lockHolder, "try", stateDir, name).CombinedOutput()
	if err != nil {
		t.Fatalf("the other command: %v\n%s", err, output)
	}
	return strings.TrimSpace(string(output))
}

// everythingOf is what a cleanup of the home task removes or retires.
func everythingOf(task homeTask) []string {
	return append([]string{task.worktree, task.scratch, state.TaskMetaPath(task.stateDir, "g1")}, task.extras...)
}

// On 2026-10-09 the supervisor resumed a paused goblin while the CFO's
// cleanup of it ran: the cleanup removed the worktree under the resume, which
// then wrote a new task record. A cleanup that finds a pause, a resume or a
// stop of its task in flight, or its record being changed as a switch and a
// restart change it, refuses before it removes anything.
func TestCleanupRefusesWhileAnotherCommandChangesTheTask(t *testing.T) {
	tests := []struct {
		name     string
		lockName string
		want     string
	}{
		{name: "a resume, a pause or a stop is in flight", lockName: state.LifecycleLockName("g1"), want: "task g1 is being paused, resumed or stopped by another command"},
		{name: "a switch or a restart is in flight", lockName: state.MetadataLockName("g1"), want: "the record of task g1 is being changed by another command"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			task := newHomeTask(t)
			heldByAnotherCommand(t, task.stateDir, test.lockName)

			// Act
			_, err := task.service.Cleanup(context.Background(), "g1")

			// Assert
			if err == nil || !strings.Contains(err.Error(), test.want) || !strings.Contains(err.Error(), "nothing was removed") {
				t.Fatalf("Cleanup = %v, want a refusal saying %q and that nothing was removed", err, test.want)
			}
			if strings.Contains(err.Error(), "\n") {
				t.Errorf("the refusal is more than one line: %q", err)
			}
			for _, path := range everythingOf(task) {
				if !exists(t, path) {
					t.Errorf("%s was removed by a cleanup that refused", path)
				}
			}
			if exists(t, filepath.Join(task.stateDir, "g1.status")) {
				t.Error("a cleanup that refused wrote to the task's status log")
			}
			if exists(t, filepath.Join(task.stateDir, state.CleanupLockName("g1"))) {
				t.Error("a cleanup that refused left its own lock held")
			}
		})
	}
}

// statusHook runs a function at the cleanup's first git status, which it
// asks for once it holds the task and before it removes anything.
type statusHook struct {
	execx.Runner
	during func()
}

func (h *statusHook) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	if hook := h.during; hook != nil && request.Name == "git" && len(request.Args) > 0 && request.Args[0] == "status" {
		h.during = nil
		hook()
	}
	return h.Runner.Run(ctx, request)
}

// The other order: while a cleanup holds the task, a resume, a pause, a stop,
// a switch and a restart each fail to take the lock they start with, so they
// change nothing, and the lock says in one line that the cleanup holds it.
func TestCleanupHoldsTheTaskAgainstEveryCommandThatChangesIt(t *testing.T) {
	// Arrange
	task := newHomeTask(t)
	refusals := map[string]string{}
	hook := &statusHook{Runner: task.service.Commands, during: func() {
		for _, name := range []string{state.LifecycleLockName("g1"), state.MetadataLockName("g1")} {
			refusals[name] = triedByAnotherCommand(t, task.stateDir, name)
		}
	}}
	task.service.Commands = hook

	// Act
	_, err := task.service.Cleanup(context.Background(), "g1")

	// Assert
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if len(refusals) != 2 {
		t.Fatalf("the cleanup never asked for the worktree's status, so nothing was tried: %v", refusals)
	}
	for name, refusal := range refusals {
		if !strings.HasPrefix(refusal, "refused: ") || !strings.Contains(refusal, state.CleanupPurpose("g1")) || strings.Contains(refusal, "\n") {
			t.Errorf("another command that tried %s got %q, want one line refusing it for %s", name, refusal, state.CleanupPurpose("g1"))
		}
	}
	for _, name := range []string{state.LifecycleLockName("g1"), state.MetadataLockName("g1"), state.CleanupLockName("g1")} {
		if got := triedByAnotherCommand(t, task.stateDir, name); got != "held" {
			t.Errorf("after the cleanup, another command that tried %s got %q, want the lock free", name, got)
		}
	}
}

// cfo kill stops a goblin and then cleans it up in its own process, while it
// still holds the task's lifecycle and record locks. Those are the cleanup's
// own caller's, so the cleanup runs, and leaves them held for the caller to
// release.
func TestCleanupRunsUnderTheLocksItsOwnProcessHoldsAsCfoKillDoes(t *testing.T) {
	// Arrange
	task := newHomeTask(t)
	held := []string{state.LifecycleLockName("g1"), state.MetadataLockName("g1")}
	for _, name := range held {
		if _, err := lock.AcquireExclusiveNamed(task.stateDir, name); err != nil {
			t.Fatal(err)
		}
	}

	// Act
	result, err := task.service.Cleanup(context.Background(), "g1")

	// Assert
	if err != nil {
		t.Fatalf("Cleanup under its own process's locks: %v", err)
	}
	for _, path := range everythingOf(task) {
		if exists(t, path) {
			t.Errorf("%s survived the cleanup\n%s", path, result.Output)
		}
	}
	for _, name := range held {
		if !lock.HeldByNamed(task.stateDir, name, os.Getpid()) {
			t.Errorf("the cleanup released %s, which its caller holds", name)
		}
		if err := lock.ReleaseExclusiveNamed(task.stateDir, name); err != nil {
			t.Errorf("the caller could not release %s after the cleanup: %v", name, err)
		}
	}
}
