package lifecycle

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/standin"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// The fixtures below run as separate processes: a terminal's host, the goblin
// in it, and a stand-in Docker Desktop the goblin starts, each reporting the
// processes it started to the file CFO_LIFECYCLE_PIDS names, one line each.
const (
	lifecycleHostState = "CFO_LIFECYCLE_HOST_STATE"
	lifecyclePIDs      = "CFO_LIFECYCLE_PIDS"
	lifecycleService   = "CFO_LIFECYCLE_SERVICE"
)

// TestLifecycleHostFixture hosts a goblin fixture in the task's worktree, as
// cfo host runs a goblin's terminal.
func TestLifecycleHostFixture(t *testing.T) {
	stateDir := os.Getenv(lifecycleHostState)
	if stateDir == "" {
		return
	}
	_ = host.Run(stateDir, host.Spec{ID: os.Getenv("CFO_LIFECYCLE_HOST_ID"), Args: []string{os.Args[0], "-test.run=^TestLifecycleGoblinFixture$"}, Dir: os.Getenv("CFO_LIFECYCLE_HOST_DIR"), Cols: 80, Rows: 25})
}

// TestLifecycleGoblinFixture is a goblin that starts Docker Desktop for a
// test and a process of its own, and waits.
func TestLifecycleGoblinFixture(t *testing.T) {
	pids := os.Getenv(lifecyclePIDs)
	if pids == "" || os.Getenv(lifecycleHostState) == "" {
		return
	}
	service := startLifecycleFixture(t, os.Getenv(lifecycleService), "^TestLifecycleServiceFixture$", "CFO_LIFECYCLE_SERVICE_RUNS=1")
	own := startLifecycleFixture(t, os.Args[0], "^TestLifecycleProcessFixture$", "CFO_LIFECYCLE_FIXTURE=1")
	appendLifecyclePIDs(t, pids, "goblin "+strconv.Itoa(os.Getpid()), "service "+strconv.Itoa(service), "own "+strconv.Itoa(own))
	time.Sleep(time.Minute)
}

// TestLifecycleServiceFixture is the stand-in Docker Desktop: it starts a
// backend of its own, as Docker Desktop starts its engine's processes.
func TestLifecycleServiceFixture(t *testing.T) {
	if os.Getenv("CFO_LIFECYCLE_SERVICE_RUNS") != "1" {
		return
	}
	backend := startLifecycleFixture(t, os.Getenv("CFO_LIFECYCLE_BINARY"), "^TestLifecycleProcessFixture$", "CFO_LIFECYCLE_FIXTURE=1")
	appendLifecyclePIDs(t, os.Getenv(lifecyclePIDs), "backend "+strconv.Itoa(backend))
	time.Sleep(time.Minute)
}

func startLifecycleFixture(t *testing.T, program, test string, env ...string) int {
	child := exec.Command(program, "-test.run="+test)
	child.Env = append(os.Environ(), env...)
	child.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	return child.Process.Pid
}

func appendLifecyclePIDs(t *testing.T, path string, lines ...string) {
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString(strings.Join(lines, "\n") + "\n"); err != nil {
		t.Fatal(err)
	}
}

// A goblin's teardown never ends a machine service the goblin started. On
// 2026-10-07 `cfo pause pd-small-cleanups` stopped Docker Desktop, which the
// goblin had started from its worktree for a test, with its build and WSL
// processes, and left the engine unreachable. Docker Desktop started from a
// worktree runs there and in the goblin's terminal's job, which ends what is
// left in it when its host ends. Stopping the goblin here ends its terminal,
// the goblin and its own process, and leaves a stand-in Docker Desktop, a copy
// of this test binary under that name, and its backend running; never the
// real Docker Desktop.
func TestStoppingAGoblinLeavesTheMachineServicesItStartedRunning(t *testing.T) {
	// Arrange
	root := t.TempDir()
	h := home.Home{Root: root, State: filepath.Join(root, "state")}
	project := filepath.Join(t.TempDir(), "app")
	meta := state.TaskMeta{ID: "g1", Backend: "native", Project: project, Worktree: filepath.Join(root, "worktrees", "app", "g1"), TaskTmp: filepath.Join(h.State, "tasktmp", "g1")}
	for _, directory := range []string{project, meta.Worktree, meta.TaskTmp} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	programs := t.TempDir()
	standin.RemoveAtCleanup(t, programs)
	binary, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	docker := filepath.Join(programs, "Docker Desktop.exe")
	if err := os.WriteFile(docker, binary, 0o755); err != nil {
		t.Fatal(err)
	}
	// Windows scans a program the first time it starts, which a loaded
	// machine can take seconds over, so the stand-in starts once first.
	if output, err := exec.Command(docker, "-test.run=^$").CombinedOutput(); err != nil {
		t.Fatalf("the stand-in Docker Desktop did not run: %v\n%s", err, output)
	}
	pids := filepath.Join(t.TempDir(), "pids")
	hostProcess := exec.Command(os.Args[0], "-test.run=^TestLifecycleHostFixture$")
	hostProcess.Env = append(os.Environ(), lifecycleHostState+"="+h.State, "CFO_LIFECYCLE_HOST_ID="+meta.ID, "CFO_LIFECYCLE_HOST_DIR="+meta.Worktree, lifecyclePIDs+"="+pids, lifecycleService+"="+docker, "CFO_LIFECYCLE_BINARY="+os.Args[0])
	hostProcess.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP, HideWindow: true}
	if err := hostProcess.Start(); err != nil {
		t.Fatal(err)
	}
	hostEnded := make(chan struct{})
	go func() { _ = hostProcess.Wait(); close(hostEnded) }()
	started := map[string]int{}
	t.Cleanup(func() {
		for _, pid := range started {
			if handle, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, uint32(pid)); err == nil {
				_ = windows.TerminateProcess(handle, 1)
				_, _ = windows.WaitForSingleObject(handle, 10000)
				_ = windows.CloseHandle(handle)
			}
		}
		_ = hostProcess.Process.Kill()
		<-hostEnded
	})
	for deadline := time.Now().Add(30 * time.Second); len(started) < 4; time.Sleep(50 * time.Millisecond) {
		if data, err := os.ReadFile(pids); err == nil {
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				if name, value, ok := strings.Cut(line, " "); ok {
					started[name], _ = strconv.Atoi(value)
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the goblin fixture reported %v, want its goblin, own process, service and backend", started)
		}
	}
	resources, err := TaskResources(t.Context(), h, meta, pipeline.Reader{Root: filepath.Join(root, "gate"), Commands: execx.OSRunner{}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()

	// Act
	stopped, _, stopErr := StopResources(ctx, resources)

	// Assert
	if stopErr != nil {
		t.Errorf("StopResources: %v (stopped %v)", stopErr, stopped)
	}
	select {
	case <-hostEnded:
	case <-time.After(15 * time.Second):
		t.Errorf("the goblin's terminal host still runs after the stop; stopped %v", stopped)
	}
	for _, name := range []string{"goblin", "own"} {
		if lifecycleRunning(started[name]) {
			t.Errorf("the goblin's %s process, pid %d, still runs after the stop; stopped %v", name, started[name], stopped)
		}
	}
	time.Sleep(time.Second)
	for _, name := range []string{"service", "backend"} {
		if !lifecycleRunning(started[name]) {
			t.Errorf("the stand-in Docker Desktop's %s process, pid %d, ended with the goblin; stopped %v", name, started[name], stopped)
		}
	}
}

// lifecycleRunning reports whether pid still runs, waiting up to two seconds
// for it to end.
func lifecycleRunning(pid int) bool {
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)
	event, _ := windows.WaitForSingleObject(handle, 2000)
	return event == uint32(windows.WAIT_TIMEOUT)
}
