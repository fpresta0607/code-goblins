package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/sandbox"
	"github.com/fpresta0607/code-goblins/internal/services"
	"github.com/fpresta0607/code-goblins/internal/state"
)

const sandboxUsage = "usage: goblins sandbox run -- <program> [<argument>...]"

// sandboxEngine is what a sandbox task's runs need of the Docker engine.
type sandboxEngine interface {
	EngineRunning(ctx context.Context) (bool, error)
	StartEngine(ctx context.Context) error
	RemoveContainer(ctx context.Context, name string) error
}

// sandboxDocker is the engine a sandbox task's runs use, which says what
// a start of it does on out: the runtime's, or else the machine's docker.
func sandboxDocker(runtime commandRuntime, out io.Writer) sandboxEngine {
	if runtime.sandboxEngine != nil {
		return runtime.sandboxEngine(out)
	}
	return services.CLI{Commands: execx.OSRunner{}, Desktop: services.WindowsDesktop, Out: out}
}

// runSandbox runs goblins sandbox run: the command after -- runs in a container
// of the task's image, with its worktree and scratch folder alone mounted,
// in the machine's heavy line as goblins gate turn runs a command, and the
// container is removed once the run ended. From before the container can
// start until it is removed, a record of it under the task's tasktmp folder
// holds the task's host git back (sandbox.RunningContainerRefusal), and a
// run first removes the container of each record whose run ended without
// removing its own. Before anything starts it has the next host command
// walk the worktree for a .git the container made, and once its container
// is removed it walks it once itself, so the host commands after it need no
// walk when it found none (sandbox.WalkForNestedGit). A task whose brief carries no
// sandbox: needed, a worktree whose .git link changed, and an engine that
// cannot run are refused, and the command then runs nowhere: never on the
// host instead.
func runSandbox(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) < 3 || args[0] != "run" || args[1] != "--" {
		fmt.Fprintln(stderr, sandboxUsage)
		return 2
	}
	command := args[2:]
	refuse := func(format string, values ...any) int {
		fmt.Fprintf(stderr, "goblins sandbox run: the command ran nowhere: "+format+"\n", values...)
		return exitNoTurn
	}
	task := os.Getenv("GOBLINS_TASK_ID")
	if task == "" {
		return refuse("this terminal names no task (GOBLINS_TASK_ID), and only a sandbox task's goblin runs commands in a container")
	}
	h, err := runtime.resolveHome()
	if err != nil {
		return refuse("%v", err)
	}
	meta, err := state.ReadTaskMeta(h.State, task)
	if err != nil {
		return refuse("read the record of task %s: %v", task, err)
	}
	if meta.SandboxImage == "" {
		return refuse("task %s's brief carries no sandbox: needed, so its commands run on this PC as every task's do: run it without goblins sandbox run", task)
	}
	if !filepath.IsAbs(meta.TaskTmp) {
		return refuse("task %s's record names no tasktmp folder, where the records of its containers are kept, but %q", task, meta.TaskTmp)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return refuse("%v", err)
	}
	if err := sandbox.CheckGitLink(meta.Worktree, cwd, meta.SandboxGitLink); err != nil {
		return refuse("%v. Tell the CEO with a blocked report; never repair the link yourself", err)
	}
	ctx := context.Background()
	engine := sandboxDocker(runtime, stderr)
	name := sandboxContainerName(task)
	docker, err := sandboxCommand(ctx, engine, sandbox.Run{Task: task, Name: name, Image: meta.SandboxImage, Checkout: meta.Worktree, Scratch: meta.Scratch, Cwd: cwd, Command: command})
	if err != nil {
		return refuse("%v", err)
	}
	if err := sandbox.ForgetNestedGitWalk(meta.TaskTmp); err != nil {
		return refuse("%v", err)
	}
	runs := filepath.Join(meta.TaskTmp, sandbox.RunsDir)
	removeEndedSandboxRuns(engine, runs, stderr)
	if err := os.MkdirAll(runs, 0o755); err != nil {
		return refuse("make the folder of the task's container records: %v", err)
	}
	if _, err := lock.AcquireExclusiveNamedFor(runs, name, "the container "+name+" of task "+task); err != nil {
		return refuse("record the container %s: %v", name, err)
	}
	exit := runGateTurn(append([]string{"--as", "sandbox " + task + ": " + strings.Join(command, " "), "--"}, docker...), stdout, stderr, runtime)
	if removeSandboxContainer(engine, name, "goblins sandbox run", stderr) {
		if err := sandbox.WalkForNestedGit(meta.TaskTmp, meta.Worktree); err != nil {
			fmt.Fprintf(stderr, "goblins sandbox run: %s\n", oneLine(err))
		}
		if err := lock.ReleaseExclusiveNamed(runs, name); err != nil {
			fmt.Fprintf(stderr, "goblins sandbox run: the container %s is removed, but its record in %s stays and holds the task's host git back: %s\n", name, runs, oneLine(err))
		}
	}
	return exit
}

// removeEndedSandboxRuns removes the container of each record in runs whose
// goblins sandbox run ended without removing it, and then the record, so the
// task's host git runs again. A record whose run still runs is left as it
// is, as is one whose container could not be removed.
func removeEndedSandboxRuns(engine sandboxEngine, runs string, stderr io.Writer) {
	entries, err := os.ReadDir(runs)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if holder, err := lock.ReadNamed(runs, entry.Name()); err == nil && holder.Alive() {
			continue
		}
		if !removeSandboxContainer(engine, entry.Name(), "goblins sandbox run", stderr) {
			continue
		}
		if err := os.Remove(filepath.Join(runs, entry.Name())); err != nil {
			fmt.Fprintf(stderr, "goblins sandbox run: removed the container %s an ended run left, but not its record: %s\n", entry.Name(), oneLine(err))
		}
	}
}

// sandboxCommand is the docker run that runs r, once the engine answers,
// starting it when it does not. An engine that cannot run is refused, so a
// sandbox task's command runs in its container or nowhere.
func sandboxCommand(ctx context.Context, engine sandboxEngine, r sandbox.Run) ([]string, error) {
	arguments, err := sandbox.DockerArguments(r)
	if err != nil {
		return nil, err
	}
	isRunning, err := engine.EngineRunning(ctx)
	if err == nil && !isRunning {
		err = engine.StartEngine(ctx)
	}
	if err != nil {
		return nil, fmt.Errorf("docker cannot run here (%v), and a sandbox task's command never runs on this PC instead", err)
	}
	return append([]string{"docker"}, arguments...), nil
}

// sandboxContainerName is the name of one run's container of task, which no
// other run's has.
func sandboxContainerName(task string) string {
	return "goblins-sandbox-" + task + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
}

// removeSandboxContainer ends and removes the container name, which its
// docker run removes as it ends, but which a docker run that was itself
// ended may have left running, and reports whether it is gone. who says it
// on stderr when it cannot.
func removeSandboxContainer(engine sandboxEngine, name, who string, stderr io.Writer) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := engine.RemoveContainer(ctx, name); err != nil {
		fmt.Fprintf(stderr, "%s: the container %s may still run: %s\n", who, name, oneLine(err))
		return false
	}
	return true
}
