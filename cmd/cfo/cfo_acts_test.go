package main

import (
	"bytes"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// inAnotherAgentsSession makes every command of this test run as in an
// agent's session that is not the CFO's own.
func inAnotherAgentsSession(t *testing.T) {
	t.Helper()
	before := notTheCFO
	notTheCFO = func(string) error {
		return fmt.Errorf("%w: this command runs under an agent harness (claude.exe pid 19524)", supervisor.ErrNotOwnSession)
	}
	t.Cleanup(func() { notTheCFO = before })
}

func TestACommandThatActsAsTheCFOIsRefusedInAnotherAgentsSession(t *testing.T) {
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
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			// Arrange
			inAnotherAgentsSession(t)
			var stdout, stderr bytes.Buffer

			// Act
			exit := runWithRuntime(args, &stdout, &stderr, testCommandRuntime(t))

			// Assert
			want := "cfo " + args[0] + ": this is not the CFO's own session, which is the agent native terminal cfo runs: this command runs under an agent harness (claude.exe pid 19524)."
			if exit != 1 || !strings.HasPrefix(stderr.String(), want) || stdout.Len() != 0 {
				t.Errorf("exit = %d, stdout = %q, stderr = %q, want it refused with %q", exit, stdout.String(), stderr.String(), want)
			}
		})
	}
}

// Reading the fleet, and the commands a goblin has for its own task, are not
// the CFO's acts: no session is refused them for not being the CFO's own.
func TestReadingTheFleetIsNotActingAsTheCFO(t *testing.T) {
	for _, args := range [][]string{
		{"drain"},
		{"reap"},
		{"reap", "--dry-run", "--json"},
		{"allowance-floor"},
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
	} {
		if actsAsCFO(args) {
			t.Errorf("cfo %s is counted as an act of the CFO's", strings.Join(args, " "))
		}
	}
}

// Every command cfo dispatches says whether it acts as the CFO: one added
// later that says neither fails here, so the rule cannot miss a command by
// nobody having thought of it.
func TestEveryCommandSaysWhetherItActsAsTheCFO(t *testing.T) {
	// Arrange
	notTheCFOs := [][]string{
		// The machine's and the Overlord's: setting the home up, running the
		// supervisor and its terminals, and showing them.
		{"install", "uninstall", "update", "home", "dev-drive", "dictation", "doctor", "serve", "stop", "status", "host", "attach", "hooks", "connection-repair", "version"},
		// Reading the fleet and a project.
		{"fleet-view", "peek", "runtime", "tickets", "project", "route", "verify", "security", "hygiene", "evidence", "process-plan"},
		// The hooks, which prove the CFO's own session themselves, and the
		// two commands that take the lock only there.
		{"hook", "native-hook", "register", "session-start", "watch"},
		// Bound to the registered CFO by its process, or to the caller the
		// supervisor proves, and only the CFO's own session registers.
		{"question", "answer", "run-request", "afk"},
		// A goblin's for its own task, and the credential commands the
		// board's own terminals run.
		{"notify", "helper", "pipeline", "gate", "worktree", "services", "present", "review", "deliver", "auth"},
	}
	sometimes := []string{"drain", "reap", "allowance-floor"}
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	_, dispatch, isFound := strings.Cut(string(source), "switch args[0] {")
	dispatch, _, isEnded := strings.Cut(dispatch, "default:")
	if !isFound || !isEnded {
		t.Fatal("main.go no longer dispatches with switch args[0] and a default arm")
	}
	var commands []string
	for _, line := range strings.Split(dispatch, "\n") {
		if names, isCase := strings.CutPrefix(strings.TrimSpace(line), "case "); isCase {
			for _, name := range strings.Split(strings.TrimSuffix(strings.TrimSpace(names), ":"), ",") {
				commands = append(commands, strings.Trim(strings.TrimSpace(name), `"`))
			}
		}
	}
	if len(commands) < 50 {
		t.Fatalf("read only %d commands from main.go's dispatch: %q", len(commands), commands)
	}

	for _, command := range commands {
		// Act
		isAct := actsAsCFO([]string{command})

		// Assert
		isNot := slices.ContainsFunc(notTheCFOs, func(group []string) bool { return slices.Contains(group, command) })
		switch {
		case slices.Contains(sometimes, command):
			if isAct || isNot {
				t.Errorf("cfo %s acts as the CFO only with some of its flags, and is counted otherwise", command)
			}
		case isAct == isNot:
			t.Errorf("cfo %s: acts as the CFO = %t, listed as not the CFO's = %t. Say which it is, in actsAsCFO or in this test", command, isAct, isNot)
		}
	}
}

// A session that may act as the CFO, its own or a terminal no agent runs, is
// refused nothing: the command runs.
func TestACommandThatActsAsTheCFORunsWhereItMay(t *testing.T) {
	// Arrange
	var stdout, stderr bytes.Buffer

	// Act
	exit := runWithRuntime([]string{"send"}, &stdout, &stderr, testCommandRuntime(t))

	// Assert
	if exit != 2 || !strings.Contains(stderr.String(), "cfo send: target is required") {
		t.Errorf("exit = %d, stderr = %q, want the command itself answering", exit, stderr.String())
	}
}
