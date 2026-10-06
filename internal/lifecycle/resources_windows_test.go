package lifecycle

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
)

type missingPaneRunner struct{ code string }

func (runner missingPaneRunner) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	if request.Name != "herdr" {
		return execx.Result{}, fmt.Errorf("unexpected command: %s", request.Name)
	}
	if strings.Contains(strings.Join(request.Args, " "), "pane get") {
		return execx.Result{ExitCode: 1, Stdout: []byte(`{"error":{"code":"` + runner.code + `"}}`)}, nil
	}
	return execx.Result{ExitCode: 1, Stderr: []byte("pane process info unavailable")}, nil
}

func TestTaskResourcesContinuesAfterConfirmedLegacyPaneAbsence(t *testing.T) {
	for _, code := range []string{"pane_not_found", "server_unavailable"} {
		t.Run(code, func(t *testing.T) {
			directory := t.TempDir()
			stateDir := filepath.Join(directory, "state")
			meta := state.TaskMeta{ID: "fixture", Backend: "herdr", HerdrSession: "missing-lifecycle-fixture", HerdrPaneID: "w1:p2", Project: directory, Worktree: filepath.Join(directory, ".worktrees", "gb-fixture"), TaskTmp: filepath.Join(stateDir, "tasktmp", "fixture")}
			resources, err := TaskResources(t.Context(), home.Home{Root: filepath.Dir(stateDir), State: stateDir}, meta, pipeline.Reader{Root: filepath.Join(directory, "gate"), Commands: missingPaneRunner{code: code}})
			if (err == nil) != (code == "pane_not_found") {
				t.Fatalf("pane evidence=%s resources=%+v error=%v", code, resources, err)
			}
			if len(resources.Hosts) != 0 || len(resources.Directories) < 2 {
				t.Fatalf("missing pane lost detached process directories: %+v", resources)
			}
		})
	}
}

type gateRunRunner struct{ status string }

func (runner gateRunRunner) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	switch request.Name {
	case "git":
		return execx.Result{Stdout: []byte("gb-fixture\n")}, nil
	case "sqlite3":
		row := fmt.Sprintf(`[{"id":"run-1","repo_id":"repo-1","branch":"gb-fixture","status":%q,"head":"%s","intent":"saved intent","worktree":""}]`, runner.status, strings.Repeat("a", 40))
		return execx.Result{Stdout: []byte(row)}, nil
	}
	return execx.Result{}, fmt.Errorf("unexpected command: %s", request.Name)
}

func TestTaskResourcesInterruptsOnlyAnOpenGateRun(t *testing.T) {
	for _, status := range []string{"completed", "failed", "cancelled", "ci_monitor_interrupted", "running"} {
		t.Run(status, func(t *testing.T) {
			directory := t.TempDir()
			stateDir := filepath.Join(directory, "state")
			gateRoot := filepath.Join(directory, "gate")
			if err := os.MkdirAll(gateRoot, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(gateRoot, "state.sqlite"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			meta := state.TaskMeta{ID: "fixture", Backend: "native", Project: directory, Worktree: filepath.Join(directory, ".worktrees", "gb-fixture"), TaskTmp: filepath.Join(stateDir, "tasktmp", "fixture")}
			resources, err := TaskResources(t.Context(), home.Home{Root: filepath.Dir(stateDir), State: stateDir}, meta, pipeline.Reader{Root: gateRoot, Commands: gateRunRunner{status: status}})
			if err != nil {
				t.Fatal(err)
			}
			if isInterrupted := resources.Gate.ID != ""; isInterrupted != (status == "running") {
				t.Fatalf("latest %s run interrupted=%v: %+v", status, isInterrupted, resources.Gate)
			}
		})
	}
}

func TestStoppingATaskKeepsAnotherTasksGateTestUnderItsGoTemp(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOCALAPPDATA", filepath.Join(root, "localappdata"))
	stateDir := filepath.Join(root, "state")
	project := filepath.Join(root, "project")
	meta := state.TaskMeta{ID: "task-a", Project: project, Worktree: filepath.Join(project, ".worktrees", "gb-task-a"), TaskTmp: filepath.Join(stateDir, "tasktmp", "task-a")}
	otherWorktree := filepath.Join(project, ".worktrees", "gb-task-b")
	goTmp, err := state.GoTmpDir(stateDir, meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	otherTestBinary := filepath.Join(goTmp, "go-build1", "b001", "task-b.test.exe")
	for _, directory := range []string{meta.Worktree, meta.TaskTmp, otherWorktree, filepath.Dir(otherTestBinary)} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	binary, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(otherTestBinary, binary, 0o755); err != nil {
		t.Fatal(err)
	}
	start := func(executable, directory string) *exec.Cmd {
		child := exec.Command(executable, "-test.run=^TestLifecycleProcessFixture$")
		child.Dir = directory
		child.Env = append(os.Environ(), "CFO_LIFECYCLE_FIXTURE=1")
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
		return child
	}
	otherGateTest := start(otherTestBinary, otherWorktree)
	taskProcess := start(os.Args[0], meta.Worktree)
	detachedProcess := startDetachedFixture(t, meta.TaskTmp)
	type processObservation struct {
		process Process
		handle  windows.Handle
		isOwned bool
	}
	var observed []processObservation
	for _, child := range []*exec.Cmd{taskProcess, detachedProcess, otherGateTest} {
		handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(child.Process.Pid))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = windows.CloseHandle(handle) })
		var creation, exit, kernel, user windows.Filetime
		if err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
			t.Fatal(err)
		}
		observed = append(observed, processObservation{process: Process{PID: child.Process.Pid, Name: filepath.Base(child.Path), Started: time.Unix(0, creation.Nanoseconds())}, handle: handle, isOwned: child != otherGateTest})
	}
	resources, err := TaskResources(t.Context(), home.Home{Root: filepath.Dir(stateDir), State: stateDir}, meta, pipeline.Reader{Root: filepath.Join(root, "gate"), Commands: execx.OSRunner{}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	stopped, teardown, stopErr := StopResources(ctx, resources)
	for _, observation := range observed {
		var exitCode uint32
		exitErr := windows.GetExitCodeProcess(observation.handle, &exitCode)
		waitResult, waitErr := windows.WaitForSingleObject(observation.handle, 0)
		evidence := fmt.Sprintf("process=%+v exit=%d exitError=%v wait=%d waitError=%v stopped=%v pending=%+v stopError=%v", observation.process, exitCode, exitErr, waitResult, waitErr, stopped, teardown, stopErr)
		t.Log(evidence)
		if exitErr != nil || waitErr != nil {
			t.Errorf("cannot prove process state: %s", evidence)
			continue
		}
		if !observation.isOwned {
			if slices.Contains(stopped, fmt.Sprintf("%s pid %d", observation.process.Name, observation.process.PID)) {
				t.Errorf("another task's gate test was falsely reported stopped: %s", evidence)
			}
			if exitCode != STILL_ACTIVE || waitResult != uint32(windows.WAIT_TIMEOUT) {
				t.Errorf("another task's gate test is not active: %s", evidence)
			}
			continue
		}
		if problem := stoppedProcessProblem(observation.process, exitCode, waitResult, teardown); problem != "" {
			t.Errorf("%s: %s", problem, evidence)
		}
		if !slices.Contains(stopped, fmt.Sprintf("%s pid %d", observation.process.Name, observation.process.PID)) {
			t.Errorf("owned process was not reported stopped: %s", evidence)
		}
	}
	for _, pending := range teardown {
		if !slices.ContainsFunc(observed, func(observation processObservation) bool {
			return observation.isOwned && observation.process.PID == pending.PID
		}) {
			t.Errorf("pending teardown names a process outside the task: pending=%+v observed=%+v stopped=%v", teardown, observed, stopped)
		}
	}
	if stopErr != nil {
		t.Errorf("Stop failed: stopped=%v pending=%+v error=%v", stopped, teardown, stopErr)
	}
}

func stoppedProcessProblem(process Process, exitCode, waitResult uint32, teardown []state.TeardownProcess) string {
	if exitCode == STILL_ACTIVE {
		return "the task's own process is still active"
	}
	hasTeardown := false
	for _, pending := range teardown {
		if pending.PID != process.PID {
			continue
		}
		if !pending.Started.Equal(process.Started) || pending.Name != process.Name {
			return "pending teardown does not match the owned process identity"
		}
		hasTeardown = true
	}
	if waitResult != windows.WAIT_OBJECT_0 && !hasTeardown {
		return "nonactive process with an unsignaled handle has no pending teardown identity"
	}
	return ""
}

func TestStoppedProcessContractRequiresNonactiveStatusAndIdentifiedTeardown(t *testing.T) {
	process := Process{PID: 42, Name: "fixture.exe", Started: time.Unix(100, 0)}
	pending := state.TeardownProcess{PID: process.PID, Started: process.Started, Name: process.Name}
	for _, testCase := range []struct {
		name         string
		exitCode     uint32
		waitResult   uint32
		teardown     []state.TeardownProcess
		shouldReject bool
	}{
		{name: "completed", exitCode: 1, waitResult: windows.WAIT_OBJECT_0},
		{name: "nonactive with identified teardown", exitCode: 1, waitResult: uint32(windows.WAIT_TIMEOUT), teardown: []state.TeardownProcess{pending}},
		{name: "completed after teardown recorded", exitCode: 1, waitResult: windows.WAIT_OBJECT_0, teardown: []state.TeardownProcess{pending}},
		{name: "nonactive with missing teardown", exitCode: 1, waitResult: uint32(windows.WAIT_TIMEOUT), shouldReject: true},
		{name: "wrong teardown birth", exitCode: 1, waitResult: uint32(windows.WAIT_TIMEOUT), teardown: []state.TeardownProcess{{PID: process.PID, Started: process.Started.Add(time.Second), Name: process.Name}}, shouldReject: true},
		{name: "wrong teardown name", exitCode: 1, waitResult: uint32(windows.WAIT_TIMEOUT), teardown: []state.TeardownProcess{{PID: process.PID, Started: process.Started, Name: "other.exe"}}, shouldReject: true},
		{name: "still active with teardown", exitCode: STILL_ACTIVE, waitResult: uint32(windows.WAIT_TIMEOUT), teardown: []state.TeardownProcess{pending}, shouldReject: true},
		{name: "still active without teardown", exitCode: STILL_ACTIVE, waitResult: uint32(windows.WAIT_TIMEOUT), shouldReject: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			problem := stoppedProcessProblem(process, testCase.exitCode, testCase.waitResult, testCase.teardown)
			if (problem != "") != testCase.shouldReject {
				t.Fatalf("stop contract rejection=%v want=%v: process=%+v exit=%d wait=%d pending=%+v problem=%q", problem != "", testCase.shouldReject, process, testCase.exitCode, testCase.waitResult, testCase.teardown, problem)
			}
		})
	}
}

// A task spawned into the home names its worktree, its extra worktrees and
// its scratch folder there, and every one of them is a directory its stop
// covers; a record naming anything else stops nothing.
func TestTaskResourcesCoverAHomeTasksWorktreesAndScratch(t *testing.T) {
	root := t.TempDir()
	h := home.Home{Root: root, State: filepath.Join(root, "state")}
	project := filepath.Join(t.TempDir(), "app")
	meta := state.TaskMeta{
		ID: "g1", Backend: "native", Project: project,
		Worktree: filepath.Join(root, "worktrees", "app", "g1"),
		Extras:   []string{filepath.Join(root, "worktrees", "app", "g1-proof")},
		Scratch:  filepath.Join(root, "scratch", "g1"),
		TaskTmp:  filepath.Join(h.State, "tasktmp", "g1"),
	}
	gate := pipeline.Reader{Root: filepath.Join(root, "gate"), Commands: execx.OSRunner{}}

	resources, err := TaskResources(t.Context(), h, meta, gate)

	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{meta.Worktree, meta.Extras[0], meta.Scratch, meta.TaskTmp} {
		if !slices.Contains(resources.Directories, want) {
			t.Errorf("directories = %v, want %s among them", resources.Directories, want)
		}
	}
	for name, change := range map[string]func(*state.TaskMeta){
		"a worktree outside the home":      func(m *state.TaskMeta) { m.Worktree = filepath.Join(root, "elsewhere", "g1") },
		"an extra of another task":         func(m *state.TaskMeta) { m.Extras = []string{filepath.Join(root, "worktrees", "app", "g2-proof")} },
		"an extra in another project":      func(m *state.TaskMeta) { m.Extras = []string{filepath.Join(root, "worktrees", "web", "g1-proof")} },
		"a scratch folder of another task": func(m *state.TaskMeta) { m.Scratch = filepath.Join(root, "scratch", "g2") },
	} {
		changed := meta
		change(&changed)
		if _, err := TaskResources(t.Context(), h, changed, gate); err == nil {
			t.Errorf("%s: TaskResources = nil, want a refusal", name)
		}
	}
}
