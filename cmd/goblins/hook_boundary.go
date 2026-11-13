package main

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/claudehook"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/pathguard"
	"github.com/fpresta0607/code-goblins/internal/sandbox"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/worktree"
)

// goblinToolRefusal is what the goblin of meta, whose harness started in
// worktreeDir, is told of one tool call, payload, which waits or is refused
// before it runs, or empty when it runs: a shell command's seal
// (goblinSeal), then the path boundary of its task (goblinPathRefusal).
func goblinToolRefusal(h home.Home, meta state.TaskMeta, worktreeDir string, payload claudehook.Payload) string {
	if isShellTool(payload.ToolName) {
		if refusal := goblinSeal(worktreeDir, meta.ID, payload.Command); refusal != "" {
			return refusal
		}
	}
	if refusal := sandboxRefusal(meta, payload); refusal != "" {
		return refusal
	}
	return goblinPathRefusal(h, meta, payload)
}

// sandboxRefusal is why a shell command of a sandbox task's goblin, meta's,
// is refused on the host, or empty when it runs: it runs anything but ceo,
// git and gh (sandbox.HostCommandRefusal), or its worktree's .git link no
// longer says what its spawn found (sandbox.CheckGitLink). A command that
// may run host git waits too while a container of the task may run
// (sandbox.RunningContainerRefusal) or a .git the container made is anywhere
// in the worktree (sandbox.NestedGitCheck), which a Codex goblin's walks
// for every time (sandbox.NestedGitRefusal), and every command that may
// write a .git, a goblins gate turn's log included, has the next walk again
// (sandbox.MayMakeRepository). Every other task's
// command, and every call that runs no command, is left to the path
// boundary.
func sandboxRefusal(meta state.TaskMeta, payload claudehook.Payload) string {
	if meta.SandboxImage == "" || !isShellTool(payload.ToolName) {
		return ""
	}
	cwd := payload.Cwd
	if cwd == "" {
		cwd = meta.Worktree
	}
	if refusal := sandbox.HostCommandRefusal(payload.Command, cwd, meta.Worktree, meta.Scratch); refusal != "" {
		return "[sandbox] " + refusal + "."
	}
	if err := sandbox.CheckGitLink(meta.Worktree, cwd, meta.SandboxGitLink); err != nil {
		return "[sandbox] this command waits, since host git must never read a repository the container wrote: " + err.Error() + ". Tell the CEO with a blocked report; never repair the link yourself."
	}
	if !sandbox.RunsNoHostGit(payload.Command) {
		if err := sandbox.RunningContainerRefusal(meta.TaskTmp); err != nil {
			return "[sandbox] this command waits, since host git must never read a repository the container wrote: " + err.Error() + "."
		}
		var nested error
		if meta.Harness == string(harness.Codex) {
			// A Codex goblin's apply_patch is outside the path boundary
			// (issue 2255), so its own edits could make a .git no walk
			// since saw.
			nested = sandbox.NestedGitRefusal(meta.Worktree)
		} else {
			nested = sandbox.NestedGitCheck(meta.TaskTmp, meta.Worktree)
		}
		if nested != nil {
			return "[sandbox] this command waits, since host git must never read a repository the container wrote: " + nested.Error() + "."
		}
	}
	// Forgotten only after the check, since a walk it made would mark clean
	// what this command then writes.
	if sandbox.MayMakeRepository(payload.Command) {
		if err := sandbox.ForgetNestedGitWalk(meta.TaskTmp); err != nil {
			return "[sandbox] this command waits, since it may write a .git the next host command must look for, and " + err.Error() + "."
		}
	}
	return ""
}

// goblinPathRefusal is why one tool call of the goblin of meta is refused by
// its task's path boundary (goblinBoundary), or empty when it may run.
func goblinPathRefusal(h home.Home, meta state.TaskMeta, payload claudehook.Payload) string {
	return goblinBoundary(h, meta, payload.Cwd).ToolRefusal(payload.ToolName, json.RawMessage(payload.ToolInput))
}

// isShellTool reports whether tool runs a command: Claude Code's Bash,
// PowerShell and Monitor, and the bash of Codex and pi.
func isShellTool(tool string) bool {
	switch tool {
	case "Bash", "bash", "PowerShell", "Monitor":
		return true
	}
	return false
}

// goblinBoundary is the path boundary of the goblin of meta, whose shell is
// in cwd: it writes in its worktree and extra worktrees, its scratch folder,
// the temporary folder every goblin shares and the caches its terminal names,
// and never reaches the credential store or ~/.ssh; a sandbox task's makes
// no .git in its worktree. A cwd its harness did not say reads as its
// worktree.
func goblinBoundary(h home.Home, meta state.TaskMeta, cwd string) pathguard.Boundary {
	writable := append([]string{meta.Worktree}, meta.Extras...)
	sharedTemp := ""
	if meta.Scratch != "" {
		sharedTemp = home.SharedTempBeside(meta.Scratch)
		writable = append(writable, meta.Scratch, sharedTemp)
	}
	writable = append(writable, h.Caches())
	var projectEnv map[string]string
	if manifest, err := worktree.Resolve(h.Data, meta.Project); err == nil {
		projectEnv = manifest.Env
	}
	for _, cache := range auth.CacheAudit(h.Caches(), projectEnv) {
		if cache.Path != "" {
			writable = append(writable, cache.Path)
		}
	}
	if cwd == "" {
		cwd = meta.Worktree
	}
	boundary := userBoundary(writable, cwd, func(name string) string {
		switch strings.ToUpper(name) {
		case "TEMP":
			return meta.Scratch
		case "TMP":
			return sharedTemp
		}
		return os.Getenv(name)
	})
	if meta.SandboxImage != "" {
		// Its host commands trust the walk its last container run ended
		// with (sandbox.NestedGitCheck), which no write of its goblin's sees.
		boundary.RepositoryFree = []string{meta.Worktree}
	}
	return boundary
}

// userBoundary is the path boundary of a session of this user that writes
// only in writable, with its shell in cwd and its variables read by getenv.
func userBoundary(writable []string, cwd string, getenv func(string) string) pathguard.Boundary {
	userHome, _ := os.UserHomeDir()
	fileStore, _ := auth.FileStoreRoot()
	return pathguard.Boundary{
		Writable: writable,
		Secrets:  pathguard.Secrets(userHome, getenv("APPDATA"), getenv("LOCALAPPDATA"), fileStore),
		Cwd:      cwd,
		UserHome: userHome,
		Getenv:   getenv,
	}
}
