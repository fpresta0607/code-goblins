package supervisor

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/host/hosttest"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

func TestOwnSessionIsTheHarnessNativeTerminalCFORuns(t *testing.T) {
	// Arrange
	store, _, _ := registerFixture(t)

	// Act
	program, err := OwnSession(store.Home.State)

	// Assert
	if err != nil || program.PID != os.Getpid() {
		t.Fatalf("OwnSession = pid %d, %v, want this process, the terminal's program", program.PID, err)
	}
}

func TestOwnSessionRefusesEverySessionButTheCFOs(t *testing.T) {
	for _, c := range []struct {
		name, want string
		arrange    func(t *testing.T, terminal hostedTerminal)
	}{
		{"one in no native terminal", "runs in no native terminal", func(t *testing.T, _ hostedTerminal) {
			t.Setenv(host.IDVariable, "")
		}},
		{"one in another native terminal", "runs in native terminal g1", func(t *testing.T, _ hostedTerminal) {
			t.Setenv(host.IDVariable, "g1")
		}},
		{"a goblin's that carries the terminal's name", "a goblin's", func(t *testing.T, _ hostedTerminal) {
			t.Setenv(harness.RoleVariable, harness.RoleGoblin)
		}},
		{"a gate agent's that carries the terminal's name", "a gate agent's", func(t *testing.T, _ hostedTerminal) {
			t.Setenv(gateAgentVariable, "1")
		}},
		// The System process is no user process's ancestor.
		{"one the terminal's program does not run", "does not run under it", func(t *testing.T, terminal hostedTerminal) {
			terminal.runs(t, 4)
		}},
		// Windows gave this process's pid to the terminal's program once, and
		// that program ended: the record's creation time tells them apart.
		{"one whose pid the terminal's program had before it", "does not run under it", func(t *testing.T, terminal hostedTerminal) {
			hosttest.Rewrite(t, terminal.stateDir, terminal.id, func(record *host.Record) {
				record.ChildStart = record.ChildStart.Add(-time.Hour)
			})
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			store, terminal, _ := registerFixture(t)
			c.arrange(t, terminal)

			// Act
			_, err := OwnSession(store.Home.State)

			// Assert
			if !errors.Is(err, ErrNotOwnSession) || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("OwnSession: %v, want it refused as not the CFO's own session, saying %q", err, c.want)
			}
		})
	}
}

// A command that acts as the CFO runs in the CFO's own session, in a goblin's
// and a gate agent's, which keep the commands they have, and is refused in
// any other session an agent harness runs.
func TestNotTheCFORefusesOnlyAnotherAgentsSession(t *testing.T) {
	for _, c := range []struct {
		name      string
		isRefused bool
		arrange   func(t *testing.T)
	}{
		{"the CFO's own session", false, func(*testing.T) {}},
		{"a goblin's", false, func(t *testing.T) { t.Setenv(harness.RoleVariable, harness.RoleGoblin) }},
		{"a gate agent's", false, func(t *testing.T) { t.Setenv(gateAgentVariable, "1") }},
		{"another agent's, in no native terminal", true, func(t *testing.T) { t.Setenv(host.IDVariable, "") }},
		{"another agent's, in another native terminal", true, func(t *testing.T) { t.Setenv(host.IDVariable, "g1") }},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			store, _, _ := registerFixture(t)
			// Claude Code sets this for every command it runs.
			t.Setenv("CLAUDECODE", "1")
			c.arrange(t)

			// Act
			err := NotTheCFO(store.Home.State)

			// Assert
			if !c.isRefused {
				if err != nil {
					t.Fatalf("NotTheCFO: %v, want the command allowed", err)
				}
				return
			}
			if !errors.Is(err, ErrNotOwnSession) || !strings.Contains(err.Error(), "under an agent harness") {
				t.Fatalf("NotTheCFO: %v, want it refused as another agent's session, naming the harness", err)
			}
		})
	}
}

// A terminal no agent harness runs, such as the Overlord's own, carries no
// mark of one: nothing there is refused as another agent's session.
func TestHarnessMarkNamesAnAgentHarnessAndNothingElse(t *testing.T) {
	process := func(names ...string) []proc.Entry {
		var ancestry []proc.Entry
		for at, name := range names {
			ancestry = append(ancestry, proc.Entry{PID: 100 + at, ExeBase: name})
		}
		return ancestry
	}
	for _, c := range []struct {
		name, want string
		ancestry   []proc.Entry
		env        []string
	}{
		{"the Overlord's own terminal", "", process("cfo.exe", "powershell.exe", "WindowsTerminal.exe", "explorer.exe"), []string{`Path=C:\bin`}},
		{"a command Claude Code runs", "claude.exe pid 102", process("cfo.exe", "bash.exe", "claude.exe", "Claude.exe"), nil},
		{"a command Codex runs", "node.exe pid 102", process("cfo.exe", "cmd.exe", "node.exe"), nil},
		{"a command whose parents were cut off", "CLAUDECODE", process("cfo.exe"), []string{"CLAUDECODE=1"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Act
			mark := harnessMark(c.ancestry, c.env)

			// Assert
			if c.want == "" && mark != "" || !strings.Contains(mark, c.want) {
				t.Errorf("harnessMark = %q, want it to name %q", mark, c.want)
			}
		})
	}
}

// The marks a serve forgets are exactly the variables the CFO-act check reads
// in a command's environment, so a supervisor started under an agent harness,
// whose parents a restart leaves cut off, gives the check nothing to refuse,
// while a command that still carries one keeps the refusal it gives today.
func TestTheMarksAServeForgetsAreTheOnesTheCheckReads(t *testing.T) {
	// Arrange: pi sets AI_AGENT for every command it runs and Claude Code sets
	// CLAUDECODE, and a supervisor started by an update from an agent's
	// terminal has its parents cut off, so nothing but its environment can
	// mark the session.
	env := []string{`Path=C:\bin`, `CFO_HOME=C:\home`}
	for _, name := range agentVariables {
		env = append(env, name+"=1")
	}

	// Act: the check before the start forgot them, and the environment the
	// start hands everything it runs after.
	before := harnessMark(nil, env)
	kept := make([]string, 0, len(env))
	for _, entry := range env {
		if name, _, _ := strings.Cut(entry, "="); !IsAgentSessionVariable(name) {
			kept = append(kept, entry)
		}
	}
	after := harnessMark(nil, kept)

	// Assert
	if !strings.Contains(before, "under an agent harness") {
		t.Fatalf("harnessMark = %q, want an agent's session refused for the marks it carries", before)
	}
	if after != "" {
		t.Errorf("harnessMark = %q after the start forgot the marks, want nothing refused", after)
	}
	// Every mark the check reads is one a serve forgets, and no other variable
	// is: a goblin's role, a gate agent's and a terminal's id name a different
	// thing, and a serve keeps them.
	for _, name := range agentVariables {
		if !IsAgentSessionVariable(name) {
			t.Errorf("IsAgentSessionVariable(%q) = false, want the mark the check reads", name)
		}
	}
	for _, name := range []string{"CFO_ROLE", gateAgentVariable, host.IDVariable, host.ProofVariable, "Path", "CFO_HOME"} {
		if IsAgentSessionVariable(name) {
			t.Errorf("IsAgentSessionVariable(%q) = true, want only the marks a harness sets", name)
		}
	}
}
