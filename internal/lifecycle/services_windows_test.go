package lifecycle

import (
	"context"
	"errors"
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

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/proc"
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
	lifecycleAway      = "CFO_LIFECYCLE_AWAY"
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
// test, a process of its own in its worktree, one in a folder that is none
// of the task's, and through a launcher that exits, one that left its
// terminal's job as well, and waits.
func TestLifecycleGoblinFixture(t *testing.T) {
	pids := os.Getenv(lifecyclePIDs)
	if pids == "" || os.Getenv(lifecycleHostState) == "" {
		return
	}
	service := startLifecycleFixture(t, os.Getenv(lifecycleService), "", "^TestLifecycleServiceFixture$", "CFO_LIFECYCLE_SERVICE_RUNS=1")
	own := startLifecycleFixture(t, os.Args[0], "", "^TestLifecycleProcessFixture$", "CFO_LIFECYCLE_FIXTURE=1")
	away := startLifecycleFixture(t, os.Args[0], os.Getenv(lifecycleAway), "^TestLifecycleProcessFixture$", "CFO_LIFECYCLE_FIXTURE=1")
	launcher := exec.Command(os.Args[0], "-test.run=^TestLifecycleLauncherFixture$")
	launcher.Env = append(os.Environ(), "CFO_LIFECYCLE_LAUNCHES=1")
	if output, err := launcher.CombinedOutput(); err != nil {
		t.Fatalf("the launcher fixture: %v\n%s", err, output)
	}
	appendLifecyclePIDs(t, pids, "goblin "+strconv.Itoa(os.Getpid()), "service "+strconv.Itoa(service), "own "+strconv.Itoa(own), "away "+strconv.Itoa(away))
	time.Sleep(time.Minute)
}

// TestLifecycleLauncherFixture starts a process the way Git Bash starts a
// browser bridge or a server left in the background: outside the terminal's
// job, in a folder that is none of the task's, and then exits, so the
// process has no living parent. Nothing but the environment it inherited
// still ties it to the terminal.
func TestLifecycleLauncherFixture(t *testing.T) {
	if os.Getenv("CFO_LIFECYCLE_LAUNCHES") != "1" {
		return
	}
	detached := exec.Command(os.Args[0], "-test.run=^TestLifecycleProcessFixture$")
	detached.Dir = os.Getenv(lifecycleAway)
	detached.Env = append(os.Environ(), "CFO_LIFECYCLE_FIXTURE=1", "CFO_LIFECYCLE_LAUNCHES=")
	detached.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_BREAKAWAY_FROM_JOB}
	if err := detached.Start(); err != nil {
		t.Fatal(err)
	}
	appendLifecyclePIDs(t, os.Getenv(lifecyclePIDs), "detached "+strconv.Itoa(detached.Process.Pid))
}

// TestLifecycleServiceFixture is the stand-in Docker Desktop: it starts a
// backend of its own, as Docker Desktop starts its engine's processes.
func TestLifecycleServiceFixture(t *testing.T) {
	if os.Getenv("CFO_LIFECYCLE_SERVICE_RUNS") != "1" {
		return
	}
	backend := startLifecycleFixture(t, os.Getenv("CFO_LIFECYCLE_BINARY"), "", "^TestLifecycleProcessFixture$", "CFO_LIFECYCLE_FIXTURE=1")
	appendLifecyclePIDs(t, os.Getenv(lifecyclePIDs), "backend "+strconv.Itoa(backend))
	time.Sleep(time.Minute)
}

// startLifecycleFixture starts a fixture process in directory, or where this
// process works when directory is empty. It stays in this process's job.
func startLifecycleFixture(t *testing.T, program, directory, test string, env ...string) int {
	child := exec.Command(program, "-test.run="+test)
	child.Dir = directory
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

// hostedGoblin is native goblin g1 of a home under root, running in its
// terminal's host, which runs the goblin fixture: the goblin, a process of
// its own in its worktree, one it started away from every folder of the
// task's, one detached from its job and its parent as well, and a stand-in
// Docker Desktop with its backend. It returns each fixture's pid by name, a
// handle holding each, and a channel closed once the host has ended.
func hostedGoblin(t *testing.T, root string) (home.Home, state.TaskMeta, map[string]int, map[string]windows.Handle, <-chan struct{}) {
	t.Helper()
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
	away := t.TempDir()
	standin.RemoveAtCleanup(t, away)
	hostProcess := exec.Command(os.Args[0], "-test.run=^TestLifecycleHostFixture$")
	hostProcess.Env = append(os.Environ(), lifecycleHostState+"="+h.State, "CFO_LIFECYCLE_HOST_ID="+meta.ID, "CFO_LIFECYCLE_HOST_DIR="+meta.Worktree, lifecyclePIDs+"="+pids, lifecycleService+"="+docker, "CFO_LIFECYCLE_BINARY="+os.Args[0], lifecycleAway+"="+away)
	hostProcess.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP, HideWindow: true}
	if err := hostProcess.Start(); err != nil {
		t.Fatal(err)
	}
	hostEnded := make(chan struct{})
	go func() { _ = hostProcess.Wait(); close(hostEnded) }()
	t.Cleanup(func() {
		_ = hostProcess.Process.Kill()
		<-hostEnded
	})
	started := map[string]int{}
	for deadline := time.Now().Add(30 * time.Second); len(started) < 6; time.Sleep(50 * time.Millisecond) {
		if data, err := os.ReadFile(pids); err == nil {
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				if name, value, ok := strings.Cut(line, " "); ok {
					started[name], _ = strconv.Atoi(value)
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the goblin fixture reported %v, want its goblin, own process, away process, detached process, service and backend", started)
		}
	}
	// A host starts its terminal's program before it records itself, so on a
	// loaded machine every fixture can have reported while the record is
	// still to come, and a stop then reads a task with no terminal at all.
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		_, err := host.ReadRecord(h.State, meta.ID)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the terminal's host never recorded itself: %v", err)
		}
	}
	// Each fixture waits a minute, and nothing ends one before the stop
	// below, so each pid still names its fixture here.
	held := map[string]windows.Handle{}
	for name, pid := range started {
		held[name] = standin.Hold(t, pid)
	}
	return h, meta, started, held, hostEnded
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
	h, meta, started, held, hostEnded := hostedGoblin(t, root)
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
		if lifecycleRunning(held[name]) {
			t.Errorf("the goblin's %s process, pid %d, still runs after the stop; stopped %v", name, started[name], stopped)
		}
	}
	time.Sleep(time.Second)
	for _, name := range []string{"service", "backend"} {
		if !lifecycleRunning(held[name]) {
			t.Errorf("the stand-in Docker Desktop's %s process, pid %d, ended with the goblin; stopped %v", name, started[name], stopped)
		}
	}
}

// A goblin's teardown ends what the goblin started in its terminal's job
// wherever it works, beside a machine service it keeps running. Keeping the
// service means the job no longer ends its processes when the terminal's
// host does, and once the host has ended nothing could read the job through
// it, so a process at work outside the task's folders was the task's by
// nothing the sweep could still read, and outlived every pause and stop.
func TestStoppingAGoblinEndsWhatItStartedOutsideItsFoldersBesideAMachineService(t *testing.T) {
	// Arrange
	root := t.TempDir()
	h, meta, started, held, hostEnded := hostedGoblin(t, root)
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
	if lifecycleRunning(held["away"]) {
		t.Errorf("the process the goblin started outside its folders, pid %d, still runs after the stop; stopped %v", started["away"], stopped)
	}
	for _, name := range []string{"service", "backend"} {
		if !lifecycleRunning(held[name]) {
			t.Errorf("the stand-in Docker Desktop's %s process, pid %d, ended with the goblin; stopped %v", name, started[name], stopped)
		}
	}
}

// A goblin's teardown ends what the goblin left detached: a process outside
// its terminal's job, at work in no folder of the task's, whose parent has
// exited. On 2026-10-09 two browser bridges like it held 3.7 GB while five
// goblins were paused for memory, because a pause read a task's processes
// from its terminal's job and its folders alone. Such a process still
// carries the proof value its terminal's host gave everything started in the
// terminal, and that is what a pause and a stop now end it by. The stand-in
// Docker Desktop carries the same value and goes on running.
func TestAGoblinsDetachedProcessEndsWithItAtPauseAndAtRetire(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		record state.Lifecycle
	}{
		{name: "pause", record: state.Lifecycle{Action: "pause", Pause: &state.PauseCondition{Reason: "memory", At: time.Now().UTC()}}},
		{name: "retire", record: state.Lifecycle{Action: "stop"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			root := t.TempDir()
			h, meta, started, held, hostEnded := hostedGoblin(t, root)
			terminal, err := host.ReadRecord(h.State, meta.ID)
			if err != nil {
				t.Fatal(err)
			}
			jobbed, err := proc.JobProcesses(terminal.HostPID)
			if err != nil {
				t.Fatal(err)
			}
			if slices.ContainsFunc(jobbed, func(member proc.Entry) bool { return member.PID == started["detached"] }) {
				t.Skipf("the detached fixture, pid %d, could not leave its terminal's job here, so nothing but the job would be tested", started["detached"])
			}
			record := testCase.record

			// Act
			_, stopped, stopErr := StopTask(t.Context(), h, meta, pipeline.Reader{Root: filepath.Join(root, "gate"), Commands: execx.OSRunner{}}, &record)

			// Assert
			if stopErr != nil {
				t.Errorf("StopTask: %v (stopped %v)", stopErr, stopped)
			}
			select {
			case <-hostEnded:
			case <-time.After(15 * time.Second):
				t.Errorf("the goblin's terminal host still runs after the %s; stopped %v", testCase.name, stopped)
			}
			for _, name := range []string{"goblin", "own", "away", "detached"} {
				if lifecycleRunning(held[name]) {
					t.Errorf("the goblin's %s process, pid %d, still runs after the %s; stopped %v", name, started[name], testCase.name, stopped)
				}
			}
			for _, name := range []string{"service", "backend"} {
				if !lifecycleRunning(held[name]) {
					t.Errorf("the stand-in Docker Desktop's %s process, pid %d, ended with the goblin; stopped %v", name, started[name], stopped)
				}
			}
		})
	}
}

// busyGate is a machine so loaded that reading the task's branch and its
// gate's state, which the no-mistakes daemon keeps busy, each answer only
// once they are given up, as on 2026-10-08 with two other sessions' gate
// runs going and Git symbolic-ref timing out.
type busyGate struct{}

func (busyGate) Run(ctx context.Context, _ execx.Request) (execx.Result, error) {
	<-ctx.Done()
	return execx.Result{}, ctx.Err()
}

// Between 14:44Z and 14:58Z on 2026-10-08 every pause failed with context
// deadline exceeded at 1 to 4 GB free, while the no-mistakes daemon ran two
// other sessions' gates: each saved its goblin's handoff, and its session
// went on running. Reading the gate's state took the whole bound on the
// stop, so nothing was ended. The goblin's terminal now ends whatever the
// gate's state meets, and the stop says what did not finish.
func TestAStopEndsTheGoblinWhileItsGateStateCannotBeRead(t *testing.T) {
	// Arrange
	root := t.TempDir()
	h, meta, started, held, hostEnded := hostedGoblin(t, root)
	gateRoot := filepath.Join(root, "gate")
	if err := os.MkdirAll(gateRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gateRoot, "state.sqlite"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var record state.Lifecycle

	// Act
	_, stopped, err := StopTask(t.Context(), h, meta, pipeline.Reader{Root: gateRoot, Commands: busyGate{}}, &record)

	// Assert
	select {
	case <-hostEnded:
	case <-time.After(15 * time.Second):
		t.Fatalf("the goblin's terminal host still runs after the stop; stopped %v, error %v", stopped, err)
	}
	if lifecycleRunning(held["goblin"]) {
		t.Errorf("the goblin, pid %d, still runs after the stop; stopped %v", started["goblin"], stopped)
	}
	if !errors.As(err, new(UnfinishedStop)) || !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want the stop's unread gate named once the terminal ended", err)
	}
}

// lifecycleRunning reports whether process still runs, waiting up to two
// seconds for it to end.
func lifecycleRunning(process windows.Handle) bool {
	event, _ := windows.WaitForSingleObject(process, 2000)
	return event == uint32(windows.WAIT_TIMEOUT)
}
