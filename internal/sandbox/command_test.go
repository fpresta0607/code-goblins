package sandbox

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestHostCommandRefusalRunsOnlyCfoGitAndGhOnTheHost(t *testing.T) {
	root := t.TempDir()
	worktree := filepath.Join(root, "worktrees", "project", "cg-x")
	scratch := filepath.Join(root, "scratch", "cg-x")
	installed := filepath.Join(root, "dev", "code-goblins", "bin", "cfo.exe")
	planted := filepath.Join(worktree, "tools", "git.exe")
	gitBash := func(path string) string {
		slashed := filepath.ToSlash(path)
		return "/" + strings.ToLower(slashed[:1]) + slashed[2:]
	}
	for _, test := range []struct {
		name, command, refusal string
	}{
		{"git", "git status", ""},
		{"git and gh", "git push -u origin HEAD && gh pr view --json url", ""},
		{"git with options after its command", "git commit -C HEAD --amend -m \"fix (scope)\"", ""},
		{"cfo by its installed path", installed + ` notify task --working "x && y"`, ""},
		{"PowerShell's call operator", `& "` + installed + `" pipeline run task --intent-file intent.md`, ""},
		{"a descriptor redirection", "git status 2>&1", ""},
		{"a contained run", "cfo sandbox run -- sh -c 'npm ci && npm test'", ""},
		{"a contained run in the background", "cfo gate turn --detach run.log -- cfo sandbox run -- npm test", ""},
		{"a single-quoted subcommand for the container", "cfo sandbox run -- sh -c 'echo $(date)'", ""},
		{"the project's tests on the host", "npm test", "only in its container"},
		{"a program after git", "git status; ./build.sh", "only in its container"},
		{"a variable set in front", "NODE_ENV=test git status", "only in its container"},
		{"a subcommand", "git commit -m \"$(cat msg)\"", "a command of its own"},
		{"a backtick", "git log `whoami`", "a command of its own"},
		{"PowerShell's grouping", "git status (npm test)", "a command of its own with ("},
		{"PowerShell's array subexpression", "git log @(./build.ps1)", "a command of its own with ("},
		{"git the worktree holds, by a relative path", "./tools/git.exe status", "the container can write it"},
		{"git the worktree holds, by its path", planted + " status", "the container can write it"},
		{"git the worktree holds, by its Git Bash path", gitBash(planted) + " status", "the container can write it"},
		{"gh the worktree's packages hold", "node_modules/.bin/gh pr view", "the container can write it"},
		{"cfo the scratch folder holds", filepath.Join(scratch, "cfo.exe") + " notify task --working x", "the container can write it"},
		{"cfo the worktree holds, started by a turn", "cfo gate turn -- ./cfo.exe sandbox run -- npm test", "the container can write it"},
		{"a heavy run on the host", "cfo gate turn -- npm test", "runs the project's own code on the host"},
		{"the gate's checks on the host", "cfo gate test", "runs the project's own code on the host"},
		{"a services stack", "cfo services up task", "runs the project's own code on the host"},
		{"pre-push checks", "cfo verify task", "runs the project's own code on the host"},
		{"the project's security tier", "cfo security cg-x", "runs the project's own code on the host"},
		{"the project's deployment", "cfo deploy cg-x", "runs the project's own code on the host"},
		{"git in a nested repository", "git -C vendor/x diff", "git -C"},
		{"git of another git folder", "git --git-dir=vendor/x/.git log", "git --git-dir"},
		{"git of another work tree", "git --work-tree vendor/x status", "git --work-tree"},
		{"git with a configuration of its own", "git -c core.pager=./build.sh log", "git -c"},
		{"git's programs from the worktree", "git --exec-path=tools status", "git --exec-path"},
		{"git's submodules", "git submodule update --init", "git submodule"},
		{"git recursing into submodules", "git status --recurse-submodules", "--recurse-submodules"},
		{"git recursing into submodules by its value", "git pull --recurse-submodules=yes", "--recurse-submodules"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			refusal := HostCommandRefusal(test.command, worktree, worktree, scratch)

			// Assert
			if test.refusal == "" && refusal != "" || test.refusal != "" && !strings.Contains(refusal, test.refusal) {
				t.Fatalf("HostCommandRefusal(%q) = %q, want %q", test.command, refusal, test.refusal)
			}
		})
	}
}
