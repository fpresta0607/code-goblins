package lifecycle

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strconv"
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
	if _, err := Terminate(t.Context(), Identity{PID: child.Process.Pid, Started: started.Add(-time.Hour)}); !errors.Is(err, ErrIdentityChanged) {
		t.Fatalf("reused PID was not refused: %v", err)
	}
	if current, alive := proc.StartTime(child.Process.Pid); !alive || !current.Equal(started) {
		t.Fatal("identity mismatch stopped an unrelated process")
	}
	if _, err := Terminate(t.Context(), Identity{PID: child.Process.Pid, Started: started}); err != nil {
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
	if pid, err := strconv.Atoi(os.Getenv("CFO_LIFECYCLE_FIXTURE_FOLLOW")); err == nil {
		handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
		if err != nil {
			t.Fatal(err)
		}
		defer windows.CloseHandle(handle)
		if _, err := fmt.Fprintln(os.Stdout, "ready"); err != nil {
			t.Fatal(err)
		}
		if result, err := windows.WaitForSingleObject(handle, windows.INFINITE); err != nil || result != windows.WAIT_OBJECT_0 {
			t.Fatalf("wait for task exit: result=%d error=%v", result, err)
		}
		if err := os.Chdir(os.Getenv("CFO_LIFECYCLE_FIXTURE_MOVE_TO")); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(time.Minute)
}

func TestTerminateChecksWhetherAnAccessDeniedProcessExitedDuringTheCall(t *testing.T) {
	cases := []struct {
		name       string
		stopErr    error
		exitDelay  time.Duration
		isExiting  bool
		shouldFail bool
	}{
		{name: "access denied and still alive", stopErr: windows.ERROR_ACCESS_DENIED, shouldFail: true},
		{name: "access denied while exiting concurrently", stopErr: windows.ERROR_ACCESS_DENIED, isExiting: true},
		{name: "access denied while still active before delayed exit", stopErr: windows.ERROR_ACCESS_DENIED, isExiting: true, exitDelay: time.Second, shouldFail: true},
		{name: "terminated but exiting slowly", isExiting: true, exitDelay: time.Second},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
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
			exitFailures := make(chan error, 1)
			_, err := terminate(t.Context(), Identity{PID: child.Process.Pid, Started: started}, func(windows.Handle, uint32) error {
				if testCase.isExiting && testCase.exitDelay == 0 {
					exitFailures <- child.Process.Kill()
				} else if testCase.isExiting {
					go func() {
						time.Sleep(testCase.exitDelay)
						exitFailures <- child.Process.Kill()
					}()
				}
				return testCase.stopErr
			}, windows.GetExitCodeProcess)
			if (err != nil) != testCase.shouldFail {
				t.Fatalf("expected failure=%t: %v", testCase.shouldFail, err)
			}
			if testCase.isExiting {
				if exitErr := <-exitFailures; exitErr != nil {
					t.Fatal(exitErr)
				}
			}
			if !testCase.shouldFail {
				handle, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(child.Process.Pid))
				if err != nil {
					t.Fatal(err)
				}
				defer windows.CloseHandle(handle)
				var code uint32
				if err := windows.GetExitCodeProcess(handle, &code); err != nil || code == 259 {
					t.Fatal("terminate reported success while the process had no exit status")
				}
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
	stopped, _, err := StopResources(ctx, Resources{Directories: []string{directory}})
	if err != nil || len(stopped) != 1 {
		t.Fatalf("stopped=%v error=%v", stopped, err)
	}
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(children[1].Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handle)
	if result, err := windows.WaitForSingleObject(handle, 0); err != nil || result != uint32(windows.WAIT_TIMEOUT) {
		t.Fatalf("unrelated sentinel stopped: result=%d error=%v", result, err)
	}
	owned, err := Inventory(ctx, []string{directory}, nil)
	if err != nil || len(owned) != 0 {
		t.Fatalf("task resources remain: %+v %v", owned, err)
	}
}

func TestStopResourcesEndsAProcessThatMovesIntoTheTaskDuringCleanup(t *testing.T) {
	directory := t.TempDir()
	start := func(dir string, env ...string) (*exec.Cmd, io.ReadCloser) {
		child := exec.Command(os.Args[0], "-test.run=^TestLifecycleProcessFixture$")
		child.Dir = dir
		child.Env = append(append(os.Environ(), "CFO_LIFECYCLE_FIXTURE=1"), env...)
		stdout, err := child.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
		return child, stdout
	}
	task, _ := start(directory)
	mover, stdout := start(t.TempDir(), "CFO_LIFECYCLE_FIXTURE_FOLLOW="+strconv.Itoa(task.Process.Pid), "CFO_LIFECYCLE_FIXTURE_MOVE_TO="+directory)
	ready := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(stdout).ReadString('\n')
		ready <- line
	}()
	select {
	case line := <-ready:
		if line != "ready\n" {
			t.Fatalf("mover did not start waiting for task exit: %q", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("mover did not become ready")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	stopped, _, err := StopResources(ctx, Resources{Directories: []string{directory}})
	if err != nil || len(stopped) != 2 {
		t.Fatalf("stopped=%v error=%v", stopped, err)
	}
	finished := make(chan error, 1)
	go func() { finished <- mover.Wait() }()
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatalf("a process that moved into the task after the first sweep survived Stop: stopped=%v", stopped)
	}
}
