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
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
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
	meta := state.TaskMeta{ID: "cg-x", Worktree: worktree, Scratch: filepath.Join(root, "scratch", "cg-x")}
	if image != "" {
		meta.SandboxImage, meta.SandboxGitLink = image, link
	}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFO_TASK_ID", "cg-x")
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
			name: "a folder the container made a repository", image: "node:22", task: "cg-x", engine: fakeSandboxEngine{isRunning: true},
			arrange: func(t *testing.T, meta state.TaskMeta) {
				planted := filepath.Join(meta.Worktree, "sub")
				if err := os.MkdirAll(filepath.Join(planted, ".git"), 0o755); err != nil {
					t.Fatal(err)
				}
				t.Chdir(planted)
			},
			refusal: "holds a .git of its own",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			engine := test.engine
			runtime, meta := sandboxHome(t, test.image, &engine)
			t.Setenv("CFO_TASK_ID", test.task)
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
			if !strings.Contains(said, "cfo sandbox run: the command ran nowhere") || !strings.Contains(said, test.refusal) {
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
	if !strings.HasPrefix(container, "cfo-sandbox-cg-x-") || !strings.HasSuffix(container, "-gate") {
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
