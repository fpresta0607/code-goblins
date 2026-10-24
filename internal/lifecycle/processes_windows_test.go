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
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	owned, err := Inventory(ctx, []string{directory}, nil, nil, "")
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

// A teardown never reads the command that asked for it or what started that
// command, so nothing it judges can end them. A plan ends nothing, and left
// out, the terminal it was asked from would read as having no host, and
// everything that terminal runs as detached from it. So a plan's reading
// holds them like any other process.
func TestOnlyAPlansReadingHoldsTheCommandThatAsked(t *testing.T) {
	// Arrange
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	isSelf := func(process Process) bool { return process.PID == os.Getpid() }
	isParent := func(process Process) bool { return process.PID == os.Getppid() }

	// Act
	teardown, teardownErr := ReadProcesses(ctx, false)
	plan, planErr := ReadAllProcesses(ctx, false)

	// Assert
	if teardownErr != nil || planErr != nil {
		t.Fatalf("ReadProcesses: %v, ReadAllProcesses: %v", teardownErr, planErr)
	}
	if slices.ContainsFunc(teardown, isSelf) || slices.ContainsFunc(teardown, isParent) {
		t.Errorf("a teardown's reading holds this process, pid %d, or its parent, pid %d", os.Getpid(), os.Getppid())
	}
	if !slices.ContainsFunc(plan, isSelf) || !slices.ContainsFunc(plan, isParent) {
		t.Errorf("a plan's reading lacks this process, pid %d, or its parent, pid %d", os.Getpid(), os.Getppid())
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
	case <-time.After(30 * time.Second):
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
			_, err := terminate(t.Context(), Identity{PID: child.Process.Pid, Started: started}, func(handle windows.Handle, _ uint32) error {
				if testCase.isExiting && testCase.exitDelay == 0 {
					// Kill only starts the exit, so the process has exited
					// during the call only once its handle is signaled.
					err := child.Process.Kill()
					if err == nil {
						if event, waitErr := windows.WaitForSingleObject(handle, uint32((30 * time.Second).Milliseconds())); waitErr != nil || event != windows.WAIT_OBJECT_0 {
							err = fmt.Errorf("the killed fixture did not exit: %d, %v", event, waitErr)
						}
					}
					exitFailures <- err
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
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
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
	owned, err := Inventory(ctx, []string{directory}, nil, nil, "")
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
	case <-time.After(time.Minute):
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
	case <-time.After(30 * time.Second):
		t.Fatalf("a process that moved into the task after the first sweep survived Stop: stopped=%v", stopped)
	}
}

// OwnerOf reads a running process's folder and environment: this test's own
// process works in its package's folder, which is a task's place here and
// no task's with no task to place it.
func TestOwnerReaderPlacesARunningProcessByTheFolderItWorksIn(t *testing.T) {
	// Arrange
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	stateDir := t.TempDir()

	// Act
	placed := ownerReader(stateDir, func() map[string][]string { return map[string][]string{"cg-here": {directory}} })(os.Getpid())
	unplaced := ownerReader(stateDir, func() map[string][]string { return nil })(os.Getpid())

	// Assert
	if placed != "cg-here" || unplaced != "" {
		t.Errorf("this process is %q with its folder a task's place and %q with none, want cg-here and nobody's", placed, unplaced)
	}
}
