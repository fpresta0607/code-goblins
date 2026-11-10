package main

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// inAnotherAgentsSession makes every command of this test run as in an
// agent's session that is not the CEO's own.
func inAnotherAgentsSession(t *testing.T) {
	t.Helper()
	before := notTheCEO
	notTheCEO = func(string) error {
		return fmt.Errorf("%w: this command runs under an agent harness (claude.exe pid 19524)", supervisor.ErrNotOwnSession)
	}
	t.Cleanup(func() { notTheCEO = before })
}

func TestACommandThatActsAsTheCEOIsRefusedInAnotherAgentsSession(t *testing.T) {
	for _, args := range [][]string{
		{"brief", "g1", "--project", `C:\project`},
		{"spawn", "g1", "--project", `C:\project`, "--brief", `C:\brief.md`},
		{"send", "g1", "merge", "main"},
		{"send", "g1", "--key", "Escape"},
		{"switch", "g1", "--harness", "codex"},
		{"title", "g1", "a title"},
		{"pause", "g1"},
		{"resume", "g1"},
		{"kill", "g1", "--reason", "done"},
		{"cleanup", "g1"},
		{"supersede", "g1", "--reason", "replaced"},
		{"backlog", "done", "g1"},
		{"pr", "check", "g1", "https://github.com/o/r/pull/1"},
		{"pr", "merge", "https://github.com/o/r/pull/1"},
		{"pr", "train", "project"},
		{"merge-local", "g1"},
		{"deploy", "g1"},
		{"drain", "--ack-through", "1000132"},
		{"drain", "--ack-through=1000132", "--ack-blocking"},
		{"drain", "-recovery-generation", "6731"},
		{"reap", "--apply"},
		{"reap", "--force", "19524"},
		{"reap", "--force=g1"},
		{"allowance-floor", "claude", "0"},
		{"start-hook", "accept", "g1", `C:\worktree\.venv\Lib\site-packages\zope.interface-6.4-py3.12-nspkg.pth`},
		{"memory", "lesson", "accept", "lesson-000000000000"},
		{"memory", "lesson", "reject", "lesson-000000000000", "--reason", "too narrow"},
		{"auth", "grant", "g1", "--codex-credits"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			// Arrange
			inAnotherAgentsSession(t)
			var stdout, stderr bytes.Buffer

			// Act
			exit := runWithRuntime(args, &stdout, &stderr, testCommandRuntime(t))

			// Assert
			want := "ceo " + args[0] + ": this is not the CEO's own session, which is the agent native terminal ceo runs: this command runs under an agent harness (claude.exe pid 19524)."
			if exit != 1 || !strings.HasPrefix(stderr.String(), want) || stdout.Len() != 0 {
				t.Errorf("exit = %d, stdout = %q, stderr = %q, want it refused with %q", exit, stdout.String(), stderr.String(), want)
			}
		})
	}
}

// Reading the fleet, and the commands a goblin has for its own task, are not
// the CEO's acts: no session is refused them for not being the CEO's own.
func TestReadingTheFleetIsNotActingAsTheCEO(t *testing.T) {
	for _, args := range [][]string{
		{"drain"},
		{"reap"},
		{"reap", "--dry-run", "--json"},
		{"allowance-floor"},
		{"memory", "recall", "harbor cache"},
		{"memory", "retrieve", "--id", "current"},
		{"fleet-view"},
		{"peek", "g1"},
		{"runtime"},
		{"process-plan"},
		{"tickets", "project"},
		{"doctor"},
		{"version"},
		{"session-start"},
		{"register"},
		{"notify", "g1", "--working", "at work"},
		{"helper", "start", "g1", "--brief", `C:\brief.md`},
		{"pipeline", "run", "g1", "--intent", "ship"},
		{"auth", "grant", "g1", "a-service"},
		{"auth", "revoke", "g1", "--codex-credits"},
		{"codex-guard", "--state", "state", "--task", "g1", "--until", "2026-10-22T12:00:13Z", "--", "exec"},
	} {
		if actsAsCEO(args) {
			t.Errorf("ceo %s is counted as an act of the CEO's", strings.Join(args, " "))
		}
	}
}

func TestCommandAuthorityMatchesReadOnlyPolicy(t *testing.T) {
	// Arrange
	notTheCEOs := [][]string{
		// The machine's and the Overlord's: setting the home up, running the
		// supervisor and its terminals, showing them, and configuring the
		// owner's private connection on this PC. None steers the fleet.
		{"install", "uninstall", "update", "home", "dev-drive", "dictation", "crawler", "doctor", "serve", "stop", "status", "host", "attach", "hooks", "connection-repair", "remote", "version"},
		// Reading the fleet and a project.
		{"fleet-view", "peek", "runtime", "tickets", "project", "route", "verify", "security", "hygiene", "evidence", "process-plan", "defender"},
		// The hooks, which prove the CEO's own session themselves, and the
		// two commands that take the lock only there.
		{"hook", "native-hook", "register", "session-start", "watch"},
		// Bound to the registered CEO by its process, or to the caller the
		// supervisor proves, and only the CEO's own session registers.
		{"question", "answer", "run-request", "afk"},
		// A goblin's for its own task, and filing a root issue, which goblins
		// do too. The credential commands the board's own terminals run are
		// among the sometimes below: authorizing paid Codex credits is the
		// CEO's act.
		{"notify", "helper", "pipeline", "gate", "worktree", "services", "present", "review", "deliver", "issue"},
		// The codex a goblin's armed codex guard runs, which only refuses.
		{"codex-guard"},
		// The desktop CI runner's: a job the supervisor starts in the line,
		// and the runner service's status.
		{"ci-runner"},
	}
	sometimes := []string{"drain", "reap", "allowance-floor", "memory", "auth"}
	commands := append([]string{"brief", "spawn", "send", "switch", "title", "pause", "resume", "kill", "cleanup", "supersede", "backlog", "pr", "merge-local", "deploy", "start-hook"}, sometimes...)
	for _, group := range notTheCEOs {
		commands = append(commands, group...)
	}

	for _, command := range commands {
		// Act
		isAct := actsAsCEO([]string{command})

		// Assert
		isNot := slices.ContainsFunc(notTheCEOs, func(group []string) bool { return slices.Contains(group, command) })
		switch {
		case slices.Contains(sometimes, command):
			if isAct || isNot {
				t.Errorf("ceo %s acts as the CEO only with some of its flags, and is counted otherwise", command)
			}
		case isAct == isNot:
			t.Errorf("ceo %s: acts as the CEO = %t, listed as not the CEO's = %t. Say which it is, in actsAsCEO or in this test", command, isAct, isNot)
		}
	}
}

// A session that may act as the CEO, its own or a terminal no agent runs, is
// refused nothing: the command runs.
func TestACommandThatActsAsTheCEORunsWhereItMay(t *testing.T) {
	// Arrange
	var stdout, stderr bytes.Buffer

	// Act
	exit := runWithRuntime([]string{"send"}, &stdout, &stderr, testCommandRuntime(t))

	// Assert
	if exit != 2 || !strings.Contains(stderr.String(), "ceo send: target is required") {
		t.Errorf("exit = %d, stderr = %q, want the command itself answering", exit, stderr.String())
	}
}
