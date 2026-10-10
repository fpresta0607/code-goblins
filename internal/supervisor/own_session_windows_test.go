package supervisor

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/host"
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
			record, err := host.ReadRecord(terminal.stateDir, terminal.id)
			if err != nil {
				t.Fatal(err)
			}
			record.ChildStart = record.ChildStart.Add(-time.Hour)
			data, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(terminal.stateDir, "hosts", terminal.id+".json"), data, 0o600); err != nil {
				t.Fatal(err)
			}
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
