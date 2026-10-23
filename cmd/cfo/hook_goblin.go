package main

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/browserlogin"
	"github.com/fpresta0607/code-goblins/internal/claudehook"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/starthook"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// goblinSealRefusal is what a goblin is told of a browser command a key
// fetch's seal refused.
const goblinSealRefusal = "A key fetch is reading in your login's browser now, so chrome-devtools-axi commands wait until it ends, within two minutes. Run this command again after that."

// hookGoblinSeal refuses each of a goblin's shell commands while a Python
// environment of its worktree holds a start hook no known package ships
// (startHookRefusal), and its chrome-devtools-axi command while a key fetch
// reads in the browser its task holds, so nothing the page shows reaches its
// transcript. Every other command passes, and so does a browser command when
// the goblin's home or task cannot be read, since no fetch can seal a
// browser no task holds. A seal that cannot be read refuses. name is the
// seal the goblin's harness runs: Claude Code's goblin-seal names the folder
// it started in and reads a refusal on stderr, and Codex's goblin-seal-codex
// and pi's goblin-seal-pi name their session's folder in the payload and
// read one on stdout.
func hookGoblinSeal(name string, stdin io.Reader, stdout, stderr io.Writer) int {
	payload, ok := claudehook.ReadPayload(stdin)
	if !ok {
		return 0
	}
	worktree, deny := payload.Cwd, func(message string) int { return claudehook.DenyPreToolOnStdout(stdout, message) }
	if name == "goblin-seal" {
		worktree, deny = os.Getenv("CLAUDE_PROJECT_DIR"), func(message string) int { return claudehook.DenyPreTool(stderr, message) }
	}
	if refusal := startHookRefusal(worktree, os.Getenv("CFO_TASK_ID")); refusal != "" {
		return deny(refusal)
	}
	if !strings.Contains(strings.ToLower(payload.Command), "chrome-devtools-axi") {
		return 0
	}
	h, err := home.Resolve()
	if err != nil {
		return 0
	}
	if isSealed, _ := browserlogin.Sealed(h.State, os.Getenv("CFO_TASK_ID"), time.Now()); !isSealed {
		return 0
	}
	return deny(goblinSealRefusal)
}

// startHookRefusal is what a goblin is told of a shell command that waits
// because a Python environment of worktree, the folder its harness started
// in, holds code that runs at every Python start and that no known
// package ships and that the CFO did not accept for the goblins of task's
// project or of the fleet, or empty when none does. The command is refused
// before anything it starts can run Python, and so is every one after it
// until the file is gone or accepted. The CFO is woken for each such file
// with the file and the package that installed it, never its content, which
// the package wrote: once while that wake waits for him, and again once he
// has acknowledged it and the file is still there, as after a goblin
// reinstalls what he removed. A file no package names yet while an install
// still writes is refused with no wake, since its RECORD may yet name it.
// Environments that cannot be read refuse too, without a wake, since what
// they hold is unknown, and so does a list of accepted hooks that cannot be.
func startHookRefusal(worktree, task string) string {
	if worktree == "" {
		return ""
	}
	hooks, err := starthook.Find(worktree)
	if err != nil {
		return "This command waits: your worktree's Python environments could not be read for code that runs at every Python start (" + err.Error() + "). Run it again once whatever writes there has ended."
	}
	if len(hooks) == 0 {
		return ""
	}
	h, homeErr := home.Resolve()
	if homeErr == nil {
		if hooks, err = starthook.Unaccepted(h.State, taskProject(h.State, task), hooks); err != nil {
			return "This command waits: the start hooks the CFO accepted could not be read (" + err.Error() + "). Run it again in a minute."
		}
		if len(hooks) == 0 {
			return ""
		}
	}
	named := make([]string, len(hooks))
	for index, hook := range hooks {
		named[index] = hook.Path + ", " + startHookPackage(hook) + ","
	}
	if !slices.ContainsFunc(hooks, func(hook starthook.Hook) bool { return !hook.Installing }) {
		return "This command waits: " + strings.Join(named, " and ") + " runs at every Python start, and no installed package names it yet, as while an install is still writing." +
			" Run the command again once the install has ended; if no package names it then, the CFO is told."
	}
	// A wake that could not be queued, as when another writer holds the
	// queue past its lock's budget, leaves the CFO untold, so the goblin is
	// asked to run the command again, which tries once more.
	isTold := homeErr == nil && state.ValidTaskID(task) == nil
	for index, hook := range hooks {
		if !isTold || hook.Installing {
			continue
		}
		detail := task + " was stopped before its next shell command: " + named[index] + " runs at every Python start of its worktree, which is how a hijacked release reads what this account can reach." +
			" Once you know it is safe, accept it with cfo start-hook accept " + task + " <file>, adding --fleet to accept it for every project, or remove the file, and the goblin's commands run again; otherwise end the task." +
			" Python that the command which installed it started may already have run it."
		_, isNew, err := wake.AppendUnlessQueued(h.State, "start-hook "+task+" "+hook.Path+" "+hook.Package, "review", task, detail)
		if err == nil && isNew {
			_, err = wake.PublishEpisode(h.State)
		}
		isTold = err == nil
	}
	stopped := "Stopped before Python could start: " + strings.Join(named, " and ") + " runs at every Python start, which is how a hijacked release reads what this account can reach."
	if !isTold {
		return stopped + " The CFO could not be told of it yet, so run the command again in a minute, which tells him; every shell command waits while it is there."
	}
	return stopped + " The CFO has been told. Every shell command waits while it is there, so end your turn and wait for his word."
}

// taskProject names task's project by its checkout's folder, as the start
// hooks the CFO accepts for one project name it, or is empty when its record
// names none.
func taskProject(stateDir, task string) string {
	meta, err := state.ReadTaskMeta(stateDir, task)
	if err != nil || meta.Project == "" {
		return ""
	}
	return filepath.Base(filepath.Clean(meta.Project))
}

// startHookPackage names the package a start hook came from.
func startHookPackage(hook starthook.Hook) string {
	if hook.Package == "" {
		return "which no installed package names"
	}
	return "from " + hook.Package
}
