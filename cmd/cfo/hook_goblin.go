package main

import (
	"io"
	"os"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/auth"
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
// browser no task holds. A seal that cannot be read refuses.
func hookGoblinSeal(stdin io.Reader, stderr io.Writer) int {
	payload, ok := claudehook.ReadPayload(stdin)
	if !ok {
		return 0
	}
	if refusal := startHookRefusal(os.Getenv("CLAUDE_PROJECT_DIR"), os.Getenv("CFO_TASK_ID")); refusal != "" {
		return claudehook.DenyPreTool(stderr, refusal)
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
	return claudehook.DenyPreTool(stderr, goblinSealRefusal)
}

// startHookRefusal is what a goblin is told of a shell command that waits
// because a Python environment of worktree, the folder its Claude Code
// started in, holds code that runs at every Python start and that no known
// package ships, or empty when none does. The command is refused before
// anything it starts can run Python, and so is every one after it until the
// file is gone. The CFO is woken once for each such file, with the file and
// the package that installed it, never its content, which the package
// wrote. Environments that cannot be read refuse too, without a wake, since
// what they hold is unknown.
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
	named := make([]string, len(hooks))
	for index, hook := range hooks {
		named[index] = hook.Path + ", " + startHookPackage(hook) + ","
	}
	if h, err := home.Resolve(); err == nil && state.ValidTaskID(task) == nil {
		for index, hook := range hooks {
			detail := task + " was stopped before its next shell command: " + named[index] + " runs at every Python start of its worktree, which is how a hijacked release reads what this account can reach." +
				" Remove the file once you know its package is safe, and the goblin's commands run again; otherwise end the task. Python that the command which installed it started may already have run it."
			if _, isNew, err := wake.AppendFirst(h.State, "start-hook "+task+" "+hook.Path+" "+hook.Package, "review", task, detail); err == nil && isNew {
				_, _ = wake.PublishEpisode(h.State)
			}
		}
	}
	return "Stopped before Python could start: " + strings.Join(named, " and ") + " runs at every Python start, which is how a hijacked release reads what this account can reach." +
		" The CFO has been told. Every shell command waits while it is there, so end your turn and wait for his word."
}

// startHookPackage names the package a start hook came from.
func startHookPackage(hook starthook.Hook) string {
	if hook.Package == "" {
		return "which no installed package names"
	}
	return "from " + hook.Package
}

// hookGoblinKeyAlarm wakes the CFO when a goblin's tool call gave back text
// holding a secret key, naming the goblin, the tool and the kind of key and
// never the key: the call's output is in the goblin's transcript already, so
// the key is exposed and a real one has to be rolled. One tool call wakes
// the CFO once.
func hookGoblinKeyAlarm(stdin io.Reader) int {
	payload, ok := claudehook.ReadPayload(stdin)
	if !ok {
		return 0
	}
	kinds := auth.ExposedKeys(payload.ToolResponse)
	task := os.Getenv("CFO_TASK_ID")
	if len(kinds) == 0 || state.ValidTaskID(task) != nil {
		return 0
	}
	h, err := home.Resolve()
	if err != nil {
		return 0
	}
	held := kinds[len(kinds)-1]
	if len(kinds) > 1 {
		held = strings.Join(kinds[:len(kinds)-1], ", ") + " and " + held
	}
	detail := task + "'s " + payload.ToolName + " output held " + held + ", so that key is in its transcript now. Treat it as exposed and roll it if it is real. The value is not repeated here."
	if _, isNew, err := wake.AppendFirst(h.State, "key-alarm "+payload.SessionID+" "+payload.ToolUseID, "review", task, detail); err == nil && isNew {
		_, _ = wake.PublishEpisode(h.State)
	}
	return 0
}
