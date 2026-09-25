// Package terminal names what the fleet needs from the program that hosts
// its goblins' terminals, so the CFO's commands depend on that contract
// rather than on one host. Herdr is the only backend today; the native ConPTY
// host joins it behind this interface, side by side until it is proven.
package terminal

import (
	"context"
	"time"

	"github.com/fpresta0607/code-goblins/internal/herdr"
)

// Backend hosts task terminals: each one a pane that starts at a shell
// prompt, runs one harness agent, takes typed input and shows a screen. It
// still speaks in Herdr's vocabulary (sessions, workspaces, tabs and panes,
// named by herdr.Target), which a second backend maps onto its own terminals.
type Backend interface {
	// EffectiveSession names the session every request routes to.
	EffectiveSession() string
	// EnsureServer starts the backend's server for that session when absent.
	EnsureServer(ctx context.Context) error
	// Preflight refuses a backend whose protocol this build cannot drive.
	Preflight(ctx context.Context) error
	// AgentKinds reports which harness kinds AgentStart can start.
	AgentKinds(ctx context.Context) (map[string]bool, error)
	// EnsureContainer returns the space task terminals are created in.
	EnsureContainer(ctx context.Context, cwd string) (herdr.Container, error)
	// CreateTask creates one labelled task terminal in cwd, at its shell prompt.
	CreateTask(ctx context.Context, container herdr.Container, label, cwd string) (herdr.Endpoint, error)
	// CloseTab closes a task terminal and everything running in it.
	CloseTab(ctx context.Context, session, tabID string) error
	// Snapshot reads the whole session's structure: its terminals and agents.
	Snapshot(ctx context.Context) (herdr.SessionSnapshot, error)

	// SendLiteral types text into the terminal without submitting it.
	SendLiteral(ctx context.Context, target herdr.Target, text string) error
	// SendKey sends one named key, such as Enter or Escape.
	SendKey(ctx context.Context, target herdr.Target, key string) error
	// Capture returns the last lines of the terminal's screen.
	Capture(ctx context.Context, target herdr.Target, lines int, ansi bool) (string, error)

	// AgentStart starts a harness agent under name in a terminal at its shell
	// prompt, with args passed through to the harness.
	AgentStart(ctx context.Context, target herdr.Target, name, kind string, args []string) error
	// AgentPrompt submits text to the terminal's agent as one prompt.
	AgentPrompt(ctx context.Context, target herdr.Target, text string) error
	// ReportAgent tells the backend which agent runs in a terminal it did not
	// detect one in.
	ReportAgent(ctx context.Context, target herdr.Target, agent, state, sessionID, sessionPath string) error
	// AgentStatus tells a missing terminal, a terminal with no agent, a live
	// agent and an unreadable answer apart.
	AgentStatus(ctx context.Context, target herdr.Target) (herdr.AgentStatus, error)
	// AgentDetail reads the agent's state and the counters that advance when
	// it accepts a prompt.
	AgentDetail(ctx context.Context, target herdr.Target) (herdr.AgentDetail, error)
	// WaitForWorking watches the agent across budget: working or blocked at
	// once, idle only when every read was idle, unknown when none was readable.
	WaitForWorking(ctx context.Context, target herdr.Target, budget time.Duration, polls int) (herdr.SubmitState, error)
	// BusyState reports the agent working, idle (a blocked agent counts as idle,
	// since it waits on a person) or unknown.
	BusyState(ctx context.Context, target herdr.Target) (herdr.BusyState, error)

	// HarnessRunning reports whether anything but the terminal's shell runs
	// in its foreground.
	HarnessRunning(ctx context.Context, target herdr.Target) (bool, error)
	// PaneProvablyDead reports only a terminal the backend can prove holds no
	// agent; one it cannot read is not dead.
	PaneProvablyDead(ctx context.Context, target herdr.Target) bool
	// PaneProcessInfo names the terminal's shell and foreground processes.
	PaneProcessInfo(ctx context.Context, target herdr.Target) (herdr.PaneProcessInfo, error)
}

var _ Backend = (*herdr.Client)(nil)

// HerdrSessions opens client in a session: a named one replaces the client's
// own, and an empty one keeps it.
func HerdrSessions(client *herdr.Client) func(session string) Backend {
	return func(session string) Backend {
		scoped := *client
		if session != "" {
			scoped.Session = session
		}
		return &scoped
	}
}
