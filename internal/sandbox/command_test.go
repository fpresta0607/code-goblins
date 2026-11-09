package sandbox

import (
	"strings"
	"testing"
)

func TestHostCommandRefusalRunsOnlyCfoGitAndGhOnTheHost(t *testing.T) {
	for _, test := range []struct {
		name, command, refusal string
	}{
		{"git", "git status", ""},
		{"git and gh", "git push -u origin HEAD && gh pr view --json url", ""},
		{"cfo by its path", `C:\dev\code-goblins\bin\cfo.exe notify task --working "x && y"`, ""},
		{"PowerShell's call operator", `& "C:\dev\code-goblins\bin\cfo.exe" pipeline run task --intent-file intent.md`, ""},
		{"a descriptor redirection", "git status 2>&1", ""},
		{"a contained run", "cfo sandbox run -- sh -c 'npm ci && npm test'", ""},
		{"a contained run in the background", "cfo gate turn --detach run.log -- cfo sandbox run -- npm test", ""},
		{"a single-quoted subcommand for the container", "cfo sandbox run -- sh -c 'echo $(date)'", ""},
		{"the project's tests on the host", "npm test", "only in its container"},
		{"a program after git", "git status; ./build.sh", "only in its container"},
		{"a variable set in front", "NODE_ENV=test git status", "only in its container"},
		{"a subcommand", "git commit -m \"$(cat msg)\"", "a command of its own"},
		{"a backtick", "git log `whoami`", "a command of its own"},
		{"a heavy run on the host", "cfo gate turn -- npm test", "runs the project's own code on the host"},
		{"the gate's checks on the host", "cfo gate test", "runs the project's own code on the host"},
		{"a services stack", "cfo services up task", "runs the project's own code on the host"},
		{"pre-push checks", "cfo verify task", "runs the project's own code on the host"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			refusal := HostCommandRefusal(test.command)

			// Assert
			if test.refusal == "" && refusal != "" || test.refusal != "" && !strings.Contains(refusal, test.refusal) {
				t.Fatalf("HostCommandRefusal(%q) = %q, want %q", test.command, refusal, test.refusal)
			}
		})
	}
}
