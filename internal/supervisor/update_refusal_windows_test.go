package supervisor

import (
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

// An update from a release is the Overlord's alone: the command is his in a
// terminal he opened from the desktop, and refused wherever anything marks
// it as an agent's, as AFK mode's switch is, or where its parents cannot be
// followed to the desktop.
func TestOnlyTheOverlordsOwnTerminalUpdatesCodeGoblins(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	primary, _, _, _ := primaryFixture(t, store)
	started := primary.Process.Start.Add(time.Millisecond)
	desktop := proc.Entry{PID: 900, ExeBase: "explorer.exe", Start: started.Add(-time.Hour)}
	terminal := proc.Entry{PID: 950, ParentPID: 900, ExeBase: "WindowsTerminal.exe", Start: started.Add(-time.Minute)}
	shell := proc.Entry{PID: 960, ParentPID: 950, ExeBase: "powershell.exe", Start: started.Add(-time.Second)}
	command := proc.Entry{PID: 970, ParentPID: 960, ExeBase: "goblins.exe", Start: started}
	cfo := proc.Entry{PID: primary.Process.PID, ExeBase: "supervisor.test.exe", Start: primary.Process.Start}
	his := []proc.Entry{command, shell, terminal, desktop}
	cases := []struct {
		name     string
		ancestry []proc.Entry
		env      []string
		refused  string
	}{
		{"his terminal", his, []string{"USERNAME=overlord"}, ""},
		{"a shell he opened from the desktop", []proc.Entry{command, shell, desktop}, nil, ""},
		{"a goblin's terminal", his, []string{harness.RoleVariable + "=" + harness.RoleGoblin}, "in a goblin's terminal"},
		{"a gate agent", his, []string{gateAgentVariable + "=1"}, "as a gate agent"},
		{"under the registered CFO", []proc.Entry{{PID: 971, ParentPID: 961, ExeBase: "goblins.exe", Start: started.Add(time.Second)}, {PID: 961, ParentPID: cfo.PID, ExeBase: "powershell.exe", Start: started}, cfo, desktop}, nil, "under the registered CFO"},
		{"a native terminal the fleet runs", his, []string{host.IDVariable + "=cfo"}, "in native terminal cfo"},
		{"a Herdr pane", his, []string{"HERDR_PANE_ID=3"}, "in a Herdr pane"},
		{"under an agent harness", []proc.Entry{command, shell, {PID: 980, ExeBase: "claude.exe", Start: started.Add(-time.Hour)}, desktop}, nil, "under an agent harness (claude.exe pid 980)"},
		{"an agent's environment", []proc.Entry{command, shell}, []string{"CLAUDECODE=1"}, "its environment carries CLAUDECODE"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Act
			err := UpdaterRefusal(store.Home.State, c.ancestry, c.env)

			// Assert
			if c.refused == "" {
				if err != nil {
					t.Fatalf("UpdaterRefusal = %v, want his own terminal accepted", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.refused) || !strings.Contains(err.Error(), "updating Code Goblins is the Supreme Overlord's alone") {
				t.Fatalf("UpdaterRefusal = %v, want a refusal naming %q", err, c.refused)
			}
		})
	}

	// The Overlord himself meets these two in a terminal of his own, as in
	// Git Bash, so each is one short sentence that says what to do.
	for name, c := range map[string]struct {
		ancestry []proc.Entry
		says     string
	}{
		"parents cut short of the desktop": {[]proc.Entry{command, shell}, "nothing says this terminal is the Overlord's own: run goblins update in PowerShell or cmd, or press Update in the Code Goblins window"},
		"a process it could not read":      {nil, "this terminal could not be read: run goblins update in PowerShell or cmd, or press Update in the Code Goblins window"},
	} {
		t.Run(name, func(t *testing.T) {
			// Act
			err := UpdaterRefusal(store.Home.State, c.ancestry, nil)

			// Assert
			if err == nil || !strings.Contains(err.Error(), c.says) {
				t.Fatalf("UpdaterRefusal = %v, want it refused saying %q", err, c.says)
			}
			assertOneShortSentence(t, err.Error())
		})
	}
}
