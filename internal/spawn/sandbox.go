package spawn

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/fpresta0607/code-goblins/internal/sandbox"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// sandboxNeeded reports whether a new task runs its commands in a container:
// its brief carries sandbox: needed, or it is a helper whose parent does,
// since a helper never runs with less containment than its parent. Such a
// task is refused when docker is not on PATH, since its commands could then
// run nowhere but the host, which it never falls back to.
func (s Service) sandboxNeeded(req Request, brief []byte) (bool, error) {
	isNeeded, err := sandbox.IsNeededBy(string(brief))
	if err != nil {
		return false, fmt.Errorf("spawn: %w", err)
	}
	if !isNeeded && req.Parent != "" {
		parent, err := state.ReadTaskMeta(s.StateDir, req.Parent)
		if err != nil {
			return false, fmt.Errorf("spawn: read the record of the helper's parent %s: %w", req.Parent, err)
		}
		isNeeded = parent.SandboxImage != ""
	}
	if !isNeeded {
		return false, nil
	}
	if _, err := exec.LookPath("docker"); err != nil {
		return false, fmt.Errorf("spawn: the task carries sandbox: needed, but docker is not on PATH, so its commands could run nowhere but the host, which a sandbox task never does: install Docker Desktop or take the line out of its brief (%w)", err)
	}
	return true, nil
}

// containWorktree records, for a sandbox task, the image its container runs,
// its project's devcontainer's as worktree holds it, and the .git link
// worktree has now, which its host commands check.
func containWorktree(meta *state.TaskMeta, worktree string) error {
	image, err := sandbox.Image(worktree)
	if err != nil {
		return fmt.Errorf("spawn: the task carries sandbox: needed, but %w", err)
	}
	link, err := sandbox.ReadGitLink(worktree)
	if err != nil {
		return fmt.Errorf("spawn: %w", err)
	}
	meta.SandboxImage, meta.SandboxGitLink = image, link
	return nil
}

// containEnvironment sets, in a sandbox task's launch environment env, what
// its host git runs with (sandbox.HostGitEnvironment), and makes the empty
// folder under its tasktmp folder that git reads hooks from. Every other
// task's is left as it is.
func containEnvironment(env map[string]string, meta state.TaskMeta) error {
	if meta.SandboxImage == "" {
		return nil
	}
	hooksOff := filepath.Join(meta.TaskTmp, sandbox.HooksOffDir)
	if err := os.MkdirAll(hooksOff, 0o755); err != nil {
		return fmt.Errorf("spawn: make the folder a sandbox task's host git reads hooks from: %w", err)
	}
	for name, value := range sandbox.HostGitEnvironment(hooksOff) {
		env[name] = value
	}
	return nil
}

// sandboxInstruction tells a sandbox task's goblin how its work runs, or is
// empty for every other task.
func sandboxInstruction(meta state.TaskMeta) string {
	if meta.SandboxImage == "" {
		return ""
	}
	return " Your brief carries sandbox: needed, so this repository's code is the adversary and never runs on this PC: run every command that builds, installs, tests or otherwise runs it as cfo sandbox run -- <command>, which runs it in a container of " + meta.SandboxImage +
		" with only your worktree and scratch folder mounted, in the machine's heavy line, one at a time, and in the background as cfo gate turn --detach <log-file> -- cfo sandbox run -- <command>." +
		" On the host your shell runs only cfo, git and gh, from your worktree, and every other command is refused, as are cfo gate test, cfo verify, cfo security, cfo deploy and cfo services, and git on another repository or its submodules. Read and edit files with your file tools. Your gate's Test step runs its command in the same container."
}
