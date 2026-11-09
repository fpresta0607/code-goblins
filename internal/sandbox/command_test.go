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
		{"git with options after its command", `git commit -C HEAD --amend -m "fix the scope; keep it"`, ""},
		{"git with a format of its own", `git log --format="%H %s" -n 3`, ""},
		{"git rebase onto main", "git rebase -i --autosquash main", ""},
		{"cfo by its installed path", installed + ` notify task --working "x && y"`, ""},
		{"a contained run", "cfo sandbox run -- sh -c 'npm ci && npm test'", ""},
		{"a contained run in the background", "cfo gate turn --detach run.log -- cfo sandbox run -- npm test", ""},
		{"a path from home", "git diff --stat ~/notes.txt", ""},
		{"PowerShell's call operator", `& "` + installed + `" pipeline run task --intent-file intent.md`, "a single &"},
		{"a descriptor redirection", "git status 2>&1", "a >"},
		{"a pipe", "gh pr view --json url | jq .url", "a |"},
		{"a variable in a quoted string for the container", "cfo sandbox run -- sh -c 'echo $(date)'", `'$' in a quoted string`},
		{"the project's tests on the host", "npm test", "only in its container"},
		{"a program after git", "git status; ./build.sh", "a ;"},
		{"a program on the next line", "git status\n./build.sh", "a line break"},
		{"a variable set in front", "NODE_ENV=test git status", "only in its container"},
		{"a subcommand", "git commit -m \"$(cat msg)\"", `'$' in a quoted string`},
		{"a backtick", "git log `whoami`", "a backtick"},
		{"PowerShell's grouping", "git status (npm test)", "a ("},
		{"PowerShell's array subexpression", "git log @(./build.ps1)", "a ("},
		{"an escaped double quote", `git status \"; npm test`, "a backslash before a quote"},
		{"an escaped single quote", `git log \' ; npm test \'`, "a backslash before a quote"},
		{"a subcommand between escaped single quotes", `git log \' $(npm test) \'`, "a backslash before a quote"},
		{"an escaped quote in a message", `git commit -m "add 3\" margin" && npm test`, `'\\' in a quoted string`},
		{"a quote of the other kind in a string", `git commit -m "it's" && npm test`, `'\'' in a quoted string`},
		{"a typographic quote PowerShell ends a string at", "git log '’;npm test;‘'", "a typographic quote"},
		{"an unterminated quote", `git commit -m "fix`, "a quote no other quote ends"},
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
		{"git in a nested repository", "git -C vendor/x diff", "git -C on the host, an option before git's command"},
		{"git of another git folder", "git --git-dir=vendor/x/.git log", "an option before git's command"},
		{"git of another work tree", "git --work-tree vendor/x status", "an option before git's command"},
		{"git with a configuration of its own", "git -c core.pager=./build.sh log", "an option before git's command"},
		{"git's programs from the worktree", "git --exec-path=tools status", "an option before git's command"},
		{"git of a bare repository in the worktree", "git --bare log", "an option before git's command"},
		{"git's configuration", "git config alias.t '!npm test'", "git config on the host, which is none of the git commands"},
		{"git's submodules", "git submodule update --init", "git submodule on the host, which is none"},
		{"git bisect running the tests", "git bisect run npm test", "git bisect on the host, which is none"},
		{"git difftool", "git difftool -x npm main", "git difftool on the host, which is none"},
		{"git grep in a pager of its own", "git grep -Onpm x", "git grep on the host, which is none"},
		{"git archive", "git archive --exec=npm HEAD", "git archive on the host, which is none"},
		{"git filter-branch", "git filter-branch --tree-filter npm HEAD", "git filter-branch on the host, which is none"},
		{"git clone", "git clone -u npm https://example.com/x.git", "git clone on the host, which is none"},
		{"a git alias", "git t", "git t on the host, which is none"},
		{"git rebase running the tests", `git rebase -x "npm test" main`, "since -x runs a command"},
		{"git rebase running the tests in a cluster", `git rebase -ix "npm test" main`, "since -x runs a command"},
		{"git rebase's exec", "git rebase --exec=npm main", "--exec starts a program"},
		{"git rebase's exec by a start of it", "git rebase --exe npm main", "--exec starts a program"},
		{"git fetch's upload pack", "git fetch --upload-pack=npm origin", "--upload-pack starts a program"},
		{"git pull's upload pack", "git pull --upload-pack npm origin main", "--upload-pack starts a program"},
		{"git push's receive pack", "git push --receive-pack=npm origin", "--receive-pack starts a program"},
		{"git push's exec", "git push --exec=npm origin", "--exec starts a program"},
		{"git recursing into submodules", "git status --recurse-submodules", "--recurse-submodules starts a program"},
		{"git recursing into submodules by its value", "git pull --recurse-submodules=yes", "--recurse-submodules starts a program"},
		{"git recursing into submodules by its other name", "git checkout --recursive main", "--recursive starts a program"},
		{"a gh alias", "gh alias set t --shell 'npm test'", "gh alias on the host"},
		{"a gh extension", "gh extension install owner/gh-x", "gh extension on the host"},
		{"a gh extension by its short name", "gh ext exec x", "gh ext on the host"},
		{"gh cloning with an upload pack", "gh repo clone owner/x -- --upload-pack=npm", "--upload-pack starts a program"},
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
