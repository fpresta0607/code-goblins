package lifecycle

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// A task an older build recorded in Herdr has a pane this build cannot
// identify, so its resources are refused by name. The directories its
// detached processes may run in are still handed back.
func TestTaskResourcesRefusesATaskRecordedInHerdr(t *testing.T) {
	directory := t.TempDir()
	stateDir := filepath.Join(directory, "state")
	meta := state.TaskMeta{ID: "fixture", Backend: "herdr", HerdrSession: "fleet", HerdrPaneID: "w1:p2", Project: directory, Worktree: filepath.Join(directory, ".worktrees", "gb-fixture"), TaskTmp: filepath.Join(stateDir, "tasktmp", "fixture")}

	resources, err := TaskResources(t.Context(), stateDir, meta, pipeline.Reader{Root: filepath.Join(directory, "gate")})

	if err == nil || !strings.Contains(err.Error(), "task fixture was recorded in Herdr by an older build") || !strings.Contains(err.Error(), "cfo cleanup fixture --force-archive") {
		t.Fatalf("TaskResources error = %v, want the Herdr record refused with how to retire it", err)
	}
	if len(resources.Hosts) != 0 || len(resources.Directories) < 2 {
		t.Fatalf("resources = %+v, want no host and the task's directories", resources)
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
	for _, status := range []string{"completed", "failed", "cancelled", "running"} {
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
			resources, err := TaskResources(t.Context(), stateDir, meta, pipeline.Reader{Root: gateRoot, Commands: gateRunRunner{status: status}})
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
	resources, err := TaskResources(t.Context(), stateDir, meta, pipeline.Reader{Root: filepath.Join(root, "gate"), Commands: execx.OSRunner{}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	stopped, _, err := StopResources(ctx, resources)
	if err != nil {
		t.Fatalf("stopped=%v error=%v", stopped, err)
	}
	hasExited := func(child *exec.Cmd, timeout time.Duration) bool {
		handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(child.Process.Pid))
		if err != nil {
			t.Fatal(err)
		}
		defer windows.CloseHandle(handle)
		result, err := windows.WaitForSingleObject(handle, uint32(timeout.Milliseconds()))
		if err != nil {
			t.Fatal(err)
		}
		return result == windows.WAIT_OBJECT_0
	}
	if !hasExited(taskProcess, 3*time.Second) {
		t.Errorf("the task's own process survived Stop: stopped=%v", stopped)
	}
	if hasExited(otherGateTest, 0) {
		t.Errorf("another task's gate test was stopped only because it runs from this task's Go temp directory: stopped=%v", stopped)
	}
}
