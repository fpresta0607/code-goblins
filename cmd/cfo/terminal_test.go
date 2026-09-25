package main

import (
	"context"
	"slices"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/terminal/terminaltest"
)

// Starting the CFO drives the backend it is given: its server, the fleet's
// space, the CFO's own terminal, the agent only when none runs there, and
// focus, so the CFO can later start on another backend the same way.
func TestStartingTheCFODrivesTheTerminalBackend(t *testing.T) {
	for name, running := range map[string]bool{"no CFO running": false, "a CFO already running": true} {
		t.Run(name, func(t *testing.T) {
			project := `C:\dev\app`
			fake := &terminaltest.Fake{
				Session:    "fleet",
				Container:  herdr.Container{Session: "fleet", WorkspaceID: "w1"},
				CFO:        herdr.Endpoint{Target: herdr.Target{Session: "fleet", Pane: "w1:p1"}, WorkspaceID: "w1", TabID: "w1:t1", PaneID: "w1:p1"},
				CFORunning: running,
			}

			started, err := startCFOWith(context.Background(), fake, project)

			if err != nil || started == running {
				t.Fatalf("startCFOWith = %v, %v; want a start reported only when none ran", started, err)
			}
			want := []string{"EnsureServer", "EnsureContainer " + project, "CFOTab " + project}
			if !running {
				want = append(want, "AgentStart fleet:w1:p1 cfo claude")
			}
			want = append(want, "Focus w1 w1:t1")
			if calls := fake.Calls(); !slices.Equal(calls, want) {
				t.Errorf("calls = %q, want %q", calls, want)
			}
		})
	}
}
