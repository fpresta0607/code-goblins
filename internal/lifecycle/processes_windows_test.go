package lifecycle

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"slices"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/proc"
)

func TestInventoryFindsADetachedProcessByWorkingDirectory(t *testing.T) {
	directory := t.TempDir()
	child := exec.Command(os.Args[0], "-test.run=^TestLifecycleProcessFixture$")
	child.Dir = directory
	child.Env = append(os.Environ(), "CFO_LIFECYCLE_FIXTURE=1")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	owned, err := Inventory(ctx, []string{directory}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(owned, func(process Process) bool { return process.PID == child.Process.Pid }) {
		t.Fatalf("fixture pid %d not found by its own cwd: %+v", child.Process.Pid, owned)
	}
	if slices.ContainsFunc(owned, func(process Process) bool { return process.PID == os.Getpid() }) {
		t.Fatal("the lifecycle controller selected itself")
	}
}

func TestTerminateChecksCreationTimeOnTheProcessHandle(t *testing.T) {
	child := exec.Command(os.Args[0], "-test.run=^TestLifecycleProcessFixture$")
	child.Env = append(os.Environ(), "CFO_LIFECYCLE_FIXTURE=1")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	started, exists := proc.StartTime(child.Process.Pid)
	if !exists {
		t.Fatal("fixture process did not start")
	}
	if err := Terminate(Identity{PID: child.Process.Pid, Started: started.Add(-time.Hour)}); !errors.Is(err, ErrIdentityChanged) {
		t.Fatalf("reused PID was not refused: %v", err)
	}
	if current, alive := proc.StartTime(child.Process.Pid); !alive || !current.Equal(started) {
		t.Fatal("identity mismatch stopped an unrelated process")
	}
	if err := Terminate(Identity{PID: child.Process.Pid, Started: started}); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- child.Wait() }()
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("verified process did not stop")
	}
}

func TestLifecycleProcessFixture(t *testing.T) {
	if os.Getenv("CFO_LIFECYCLE_FIXTURE") != "1" {
		return
	}
	time.Sleep(time.Minute)
}

func TestTerminateChecksWhetherAnAccessDeniedProcessExitedDuringTheCall(t *testing.T) {
	for _, exits := range []bool{false, true} {
		t.Run(map[bool]string{false: "still alive", true: "exited concurrently"}[exits], func(t *testing.T) {
			child := exec.Command(os.Args[0], "-test.run=^TestLifecycleProcessFixture$")
			child.Env = append(os.Environ(), "CFO_LIFECYCLE_FIXTURE=1")
			if err := child.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
			started, exists := proc.StartTime(child.Process.Pid)
			if !exists {
				t.Fatal("fixture did not start")
			}
			err := terminate(Identity{PID: child.Process.Pid, Started: started}, func(handle windows.Handle, exitCode uint32) error {
				if exits {
					if err := windows.TerminateProcess(handle, exitCode); err != nil {
						t.Fatal(err)
					}
				}
				return windows.ERROR_ACCESS_DENIED
			})
			if (err == nil) != exits {
				t.Fatalf("concurrent exit=%t: %v", exits, err)
			}
		})
	}
}

func TestStopResourcesEndsDetachedTaskProcessesAndKeepsASentinel(t *testing.T) {
	directory := t.TempDir()
	var children []*exec.Cmd
	for _, dir := range []string{directory, t.TempDir()} {
		child := exec.Command(os.Args[0], "-test.run=^TestLifecycleProcessFixture$")
		child.Dir = dir
		child.Env = append(os.Environ(), "CFO_LIFECYCLE_FIXTURE=1")
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		children = append(children, child)
		t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stopped, err := StopResources(ctx, Resources{Directories: []string{directory}})
	if err != nil || len(stopped) != 1 {
		t.Fatalf("stopped=%v error=%v", stopped, err)
	}
	if _, alive := proc.StartTime(children[1].Process.Pid); !alive {
		t.Fatal("unrelated sentinel stopped")
	}
	owned, err := Inventory(ctx, []string{directory}, nil)
	if err != nil || len(owned) != 0 {
		t.Fatalf("task resources remain: %+v %v", owned, err)
	}
}
