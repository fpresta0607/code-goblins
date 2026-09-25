package supervisor

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/terminal"
	"github.com/fpresta0607/code-goblins/internal/terminal/terminaltest"
)

// The board checks the registered CFO through the terminal backend opened in
// the CFO's own session: its agent and terminal in the session's structure,
// then the process in the terminal's foreground.
func TestTheRegisteredCFOIsCheckedThroughTheTerminalBackend(t *testing.T) {
	for name, c := range map[string]struct {
		gone bool
		want string
	}{
		"the CFO in its terminal":    {},
		"the CFO gone from the pane": {gone: true, want: "no longer owns its pane"},
	} {
		t.Run(name, func(t *testing.T) {
			store, _ := testStore(t)
			primary, _, _, _ := primaryFixture(t, store)
			foreground := primary.Process.PID
			if c.gone {
				foreground = 7 // the terminal's shell is back in the foreground
			}
			fake := &terminaltest.Fake{
				Structure: herdr.SessionSnapshot{
					Panes:  []herdr.SnapshotPane{{ID: "w1:p1", TabID: "w1:t1", WorkspaceID: "w1", TerminalID: "test-terminal"}},
					Agents: []herdr.SnapshotAgent{{PaneID: "w1:p1", TabID: "w1:t1", WorkspaceID: "w1", Agent: "codex"}},
				},
				Process: herdr.PaneProcessInfo{ShellPID: 7, ForegroundProcessGroupID: foreground},
			}
			var opened []string
			connection := &CFOConnection{State: store.Home.State, Terminals: func(session string) terminal.Backend {
				opened = append(opened, session)
				return fake
			}}

			err := connection.check(context.Background())

			if c.want == "" && err != nil || c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)) {
				t.Fatalf("check = %v, want %q", err, c.want)
			}
			if !slices.Equal(opened, []string{"isolated"}) {
				t.Errorf("opened the backend in %q, want once in the registered session", opened)
			}
			if missing := fake.Missing("Snapshot", "PaneProcessInfo isolated:w1:p1"); missing != "" {
				t.Errorf("no %q call in order: %q", missing, fake.Calls())
			}
		})
	}
}
