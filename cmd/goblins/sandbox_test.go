package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/sandbox"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// fakeSandboxEngine is a Docker engine that answers, or does not, and can
// be started, or cannot, and records what it was asked.
type fakeSandboxEngine struct {
	isRunning bool
	startErr  error
	started   int
	removed   []string
}

func (e *fakeSandboxEngine) EngineRunning(context.Context) (bool, error) { return e.isRunning, nil }

func (e *fakeSandboxEngine) StartEngine(context.Context) error {
	e.started++
	if e.startErr == nil {
		e.isRunning = true
	}
	return e.startErr
}

func (e *fakeSandboxEngine) RemoveContainer(_ context.Context, name string) error {
	e.removed = append(e.removed, name)
	return nil
}

// sandboxHome is a home with one task, cg-x, whose worktree holds a .git
// link, recorded as its sandbox link with image when image is set.
func sandboxHome(t *testing.T, image string, engine *fakeSandboxEngine) (commandRuntime, state.TaskMeta) {
	t.Helper()
	root := t.TempDir()
	h := home.Home{Root: root, State: filepath.Join(root, "state"), Data: filepath.Join(root, "data")}
	worktree := filepath.Join(root, "worktrees", "project", "cg-x")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(h.State, 0o700); err != nil {
		t.Fatal(err)
	}
	link := "gitdir: " + filepath.ToSlash(filepath.Join(root, "project", ".git", "worktrees", "cg-x"))
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte(link+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	meta := state.TaskMeta{ID: "cg-x", Worktree: worktree, Scratch: filepath.Join(root, "scratch", "cg-x"), TaskTmp: filepath.Join(h.State, "tasktmp", "cg-x")}
	if image != "" {
		meta.SandboxImage, meta.SandboxGitLink = image, link
	}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOBLINS_TASK_ID", "cg-x")
	t.Chdir(worktree)
	return commandRuntime{
		resolveHome:   func() (home.Home, error) { return h, nil },
		sandboxEngine: func(io.Writer) sandboxEngine { return engine },
	}, meta
}

func TestSandboxRunRunsNothingItCannotContain(t *testing.T) {
	for _, test := range []struct {
		name    string
		image   string
		task    string
		engine  fakeSandboxEngine
		arrange func(t *testing.T, meta state.TaskMeta)
		refusal string
	}{
		{name: "a terminal of no task", image: "node:22", engine: fakeSandboxEngine{isRunning: true}, refusal: "names no task"},
		{name: "a task without sandbox: needed", task: "cg-x", engine: fakeSandboxEngine{isRunning: true}, refusal: "carries no sandbox: needed"},
		{name: "an engine that cannot start", image: "node:22", task: "cg-x", engine: fakeSandboxEngine{startErr: errors.New("Docker Desktop is not installed")}, refusal: "never runs on this PC instead"},
		{
			name: "a .git link the container replaced", image: "node:22", task: "cg-x", engine: fakeSandboxEngine{isRunning: true},
			arrange: func(t *testing.T, meta state.TaskMeta) {
				if err := os.WriteFile(filepath.Join(meta.Worktree, ".git"), []byte("gitdir: "+filepath.Join(meta.Worktree, "planted")+"\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			refusal: "the link its spawn recorded",
		},
		{
			name: "a task whose record names no tasktmp folder", image: "node:22", task: "cg-x", engine: fakeSandboxEngine{isRunning: true},
			arrange: func(t *testing.T, meta state.TaskMeta) {
				meta.TaskTmp = ""
				if err := state.WriteTaskMeta(filepath.Dir(filepath.Dir(filepath.Dir(meta.Worktree)))+string(filepath.Separator)+"state", meta); err != nil {
					t.Fatal(err)
				}
			},
			refusal: "names no tasktmp folder",
		},
		{
			name: "a .gitmodules the container wrote", image: "node:22", task: "cg-x", engine: fakeSandboxEngine{isRunning: true},
			arrange: func(t *testing.T, meta state.TaskMeta) {
				if err := os.WriteFile(filepath.Join(meta.Worktree, ".gitmodules"), []byte("[submodule \"sub\"]\n\tpath = sub\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			refusal: "cannot use submodules",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			engine := test.engine
			runtime, meta := sandboxHome(t, test.image, &engine)
			t.Setenv("GOBLINS_TASK_ID", test.task)
			if test.arrange != nil {
				test.arrange(t, meta)
			}
			var stdout, stderr bytes.Buffer

			// Act
			exit := runSandbox([]string{"run", "--", "npm", "test"}, &stdout, &stderr, runtime)

			// Assert
			if exit != exitNoTurn {
				t.Fatalf("exit = %d, want %d; stderr: %s", exit, exitNoTurn, stderr.String())
			}
			said := stderr.String()
			if !strings.Contains(said, "goblins sandbox run: the command ran nowhere") || !strings.Contains(said, test.refusal) {
				t.Fatalf("stderr = %q, want the refusal %q", said, test.refusal)
			}
			if len(engine.removed) != 0 {
				t.Fatalf("a refused run removed containers %q, though it started none", engine.removed)
			}
		})
	}
}

func TestSandboxRunTakesItsUsageOnly(t *testing.T) {
	for _, args := range [][]string{nil, {"run"}, {"run", "--"}, {"run", "npm", "test"}, {"start", "--", "npm"}} {
		t.Run(fmt.Sprint(args), func(t *testing.T) {
			// Arrange
			var stdout, stderr bytes.Buffer

			// Act
			exit := runSandbox(args, &stdout, &stderr, commandRuntime{})

			// Assert
			if exit != 2 || !strings.Contains(stderr.String(), sandboxUsage) {
				t.Fatalf("runSandbox(%q) = %d, %q, want 2 and the usage", args, exit, stderr.String())
			}
		})
	}
}

func TestGateTestStepOfASandboxTaskRunsInItsContainer(t *testing.T) {
	// Arrange
	engine := fakeSandboxEngine{}
	runtime, meta := sandboxHome(t, "node:22", &engine)
	checkout := t.TempDir()
	command := []string{`C:\WINDOWS\system32\cmd.exe`, "/c", "npm ci && npm test"}

	// Act
	contained, container, err := gateSandboxCommand(context.Background(), &engine, checkout, meta.ID, nil, command, runtime)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if engine.started != 1 {
		t.Fatalf("the engine was started %d times, want once, as it did not answer", engine.started)
	}
	if !strings.HasPrefix(container, "goblins-sandbox-cg-x-") || !strings.HasSuffix(container, "-gate") {
		t.Fatalf("container = %q, want a gate container of cg-x", container)
	}
	want := []string{"docker", "run", "--rm", "--init", "--name", container}
	if !slices.Equal(contained[:len(want)], want) {
		t.Fatalf("command = %q, want it to start %q", contained, want)
	}
	if !slices.Contains(contained, "type=bind,source="+checkout+",target=/work") {
		t.Fatalf("command = %q, want the gate's checkout mounted", contained)
	}
	if tail := contained[len(contained)-4:]; !slices.Equal(tail, []string{"node:22", "sh", "-c", "npm ci && npm test"}) {
		t.Fatalf("command ends %q, want the step's command in the image's shell", tail)
	}
}

func TestGateTestStepRunsOnTheHostOnlyForATaskThatIsNoSandboxTask(t *testing.T) {
	command := []string{"cmd.exe", "/c", "go test ./..."}
	for _, test := range []struct {
		name     string
		image    string
		task     string
		taskErr  error
		engine   fakeSandboxEngine
		refusal  string
		isOnHost bool
	}{
		{name: "a task without sandbox: needed", task: "cg-x", isOnHost: true},
		{name: "a run of no fleet task", taskErr: fmt.Errorf("%w: its source worktree belongs to no fleet task", errNoFleetTask), isOnHost: true},
		{name: "a run outside the fleet's worktrees", isOnHost: true},
		{name: "a run whose task cannot be read", taskErr: errors.New("cannot inspect the gate's source worktrees"), refusal: "cannot tell whether the run's task runs its commands in a container"},
		{name: "a task with no record", task: "cg-gone", refusal: "cannot tell whether task cg-gone runs its commands in a container"},
		{name: "a sandbox task whose engine cannot start", image: "node:22", task: "cg-x", engine: fakeSandboxEngine{startErr: errors.New("no engine")}, refusal: "never runs on this PC instead"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			engine := test.engine
			runtime, _ := sandboxHome(t, test.image, &engine)

			// Act
			got, container, err := gateSandboxCommand(context.Background(), &engine, t.TempDir(), test.task, test.taskErr, command, runtime)

			// Assert
			if test.isOnHost {
				if err != nil || container != "" || !slices.Equal(got, command) {
					t.Fatalf("gateSandboxCommand = %q, %q, %v, want the step's own command", got, container, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.refusal) || got != nil {
				t.Fatalf("gateSandboxCommand = %q, %v, want no command and the refusal %q", got, err, test.refusal)
			}
		})
	}
}

// fakeDocker puts first on PATH a docker that writes the arguments it was
// started with to the file it returns, and exits with exit.
func fakeDocker(t *testing.T, exit int) string {
	t.Helper()
	dir := t.TempDir()
	record := filepath.Join(dir, "arguments.txt")
	script := "@echo %*> \"" + record + "\"\r\n@exit /b " + strconv.Itoa(exit) + "\r\n"
	if err := os.WriteFile(filepath.Join(dir, "docker.bat"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return record
}

// dockerRun is what the fake docker was started with, and the container it
// was asked to name.
func dockerRun(t *testing.T, record string) (string, string) {
	t.Helper()
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("docker was not started: %v", err)
	}
	arguments := strings.TrimSpace(string(data))
	_, after, _ := strings.Cut(arguments, "--name ")
	name, _, _ := strings.Cut(after, " ")
	return arguments, name
}

// A sandbox task's command runs as docker run in its image, with its
// worktree and scratch folder mounted, takes the docker run's exit code, and
// leaves no container behind. Its container is recorded while it runs, which
// holds the task's host git back, and a container a run that ended left is
// removed first. A .git the container made, which holds host git back, does
// not hold back the run that removes it.
func TestSandboxRunRunsTheCommandInItsContainerAndRemovesIt(t *testing.T) {
	// Arrange
	turnLine(t)
	engine := fakeSandboxEngine{isRunning: true}
	sandboxed, meta := sandboxHome(t, "node:22", &engine)
	runtime := turnRuntime()
	runtime.resolveHome, runtime.sandboxEngine = sandboxed.resolveHome, sandboxed.sandboxEngine
	record := fakeDocker(t, 3)
	runs := filepath.Join(meta.TaskTmp, sandbox.RunsDir)
	listing := filepath.Join(t.TempDir(), "records.txt")
	script := "@dir /b \"" + runs + "\"> \"" + listing + "\"\r\n@echo %*> \"" + record + "\"\r\n@exit /b 3\r\n"
	if err := os.WriteFile(filepath.Join(filepath.Dir(record), "docker.bat"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	holdSandboxRun(t, meta, "goblins-sandbox-cg-x-left", false)
	makeNestedGit(t, meta.Worktree)
	stdout, err := os.Create(filepath.Join(t.TempDir(), "stdout"))
	if err != nil {
		t.Fatal(err)
	}
	defer stdout.Close()
	stderr, err := os.Create(filepath.Join(t.TempDir(), "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	defer stderr.Close()

	// Act
	exit := runSandbox([]string{"run", "--", "npm", "test"}, stdout, stderr, runtime)

	// Assert
	said, _ := os.ReadFile(stderr.Name())
	if exit != 3 {
		t.Fatalf("exit = %d, want 3, the docker run's; stderr: %s", exit, said)
	}
	arguments, name := dockerRun(t, record)
	for _, want := range []string{
		"run --rm --init --name goblins-sandbox-cg-x-",
		"type=bind,source=" + meta.Worktree + ",target=/work ",
		"type=bind,source=" + filepath.Join(meta.Worktree, ".git") + ",target=/work/.git,readonly",
		"type=bind,source=" + meta.Scratch + ",target=/scratch ",
		" node:22 npm test",
	} {
		if !strings.Contains(arguments, want) {
			t.Fatalf("docker %s, want %q in it", arguments, want)
		}
	}
	if !slices.Equal(engine.removed, []string{"goblins-sandbox-cg-x-left", name}) {
		t.Fatalf("removed containers %q, want the one an ended run left, then the run's own, %q", engine.removed, name)
	}
	recorded, err := os.ReadFile(listing)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(recorded)) != name {
		t.Fatalf("records while the container ran = %q, want the run's own, %q", recorded, name)
	}
	if left, err := os.ReadDir(runs); err != nil || len(left) != 0 {
		t.Fatalf("records after the run = %v (%v), want none", left, err)
	}
}

// A gate's Test step of a sandbox task, which the gate runs as goblins gate turn
// --gate-run, starts the docker run of its command in the task's image, with
// the gate's checkout mounted, takes its exit code, and removes its
// container once it ended.
func TestGateTurnRunsASandboxTasksTestStepInItsContainer(t *testing.T) {
	// Arrange
	project, gateRoot, worktree := gateTaskFixture(t, "cg-example")
	owner := filepath.Join(project, ".worktrees", "gb-cg-example")
	if err := state.WriteTaskMeta(filepath.Join(os.Getenv("GOBLINS_HOME"), "state"), state.TaskMeta{ID: "cg-example", Project: project, Worktree: owner, SandboxImage: "node:22"}); err != nil {
		t.Fatal(err)
	}
	comparison := writeGateSource(t, gateRoot, project, worktree, "feature")
	t.Setenv("NM_HOME", gateRoot)
	turnLine(t)
	record := fakeDocker(t, 3)
	engine := &fakeSandboxEngine{isRunning: true}
	runtime := turnRuntime()
	runtime.sandboxEngine = func(io.Writer) sandboxEngine { return engine }
	var stdout, stderr lockedBuffer

	// Act
	exit := gateTurn(runtime, &stdout, &stderr, "--gate-run", "run", "--test-comparison-sha", comparison, "--", "cmd.exe", "/c", "npm test")

	// Assert
	if exit != 3 {
		t.Fatalf("exit = %d, want 3, the docker run's; stderr: %s", exit, stderr.String())
	}
	arguments, name := dockerRun(t, record)
	for _, want := range []string{"run --rm --init --name goblins-sandbox-cg-example-", "type=bind,source=" + worktree + ",target=/work ", ` node:22 sh -c "npm test"`} {
		if !strings.Contains(arguments, want) {
			t.Fatalf("docker %s, want %q in it", arguments, want)
		}
	}
	if !strings.HasSuffix(name, "-gate") || !slices.Equal(engine.removed, []string{name}) {
		t.Fatalf("removed containers %q, want the step's own, %q", engine.removed, name)
	}
}
