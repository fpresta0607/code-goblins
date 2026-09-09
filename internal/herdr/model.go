// Package herdr provides the Windows subprocess-first client for a flat
// Herdr workspace and its task panes.
package herdr

import (
	"fmt"
)

// Target identifies one pane in one Herdr session.
type Target struct {
	Session string
	Pane    string
}

// String returns the canonical session:pane form. Pane identifiers can contain
// colons, so callers must parse only the first colon in this value.
func (t Target) String() string {
	return t.Session + ":" + t.Pane
}

// Container is the one flat Herdr workspace assigned to a CFO home.
type Container struct {
	Session          string
	WorkspaceID      string
	SeededDefaultTab string
}

// Endpoint is the complete Herdr address recorded for one task.
type Endpoint struct {
	Target      Target
	WorkspaceID string
	TabID       string
	PaneID      string
}

// AgentStatus is a conservative recovery-grade agent liveness result.
type AgentStatus string

const (
	// AgentMissing means the pane itself is structurally absent.
	AgentMissing AgentStatus = "missing"
	// AgentDead means the pane exists but has no registered agent.
	AgentDead AgentStatus = "dead"
	// AgentAlive means the pane has a registered agent in a recognized status.
	AgentAlive AgentStatus = "alive"
	// AgentUnreadable means Herdr could not provide a trustworthy answer.
	AgentUnreadable AgentStatus = "unreadable"
)

// BusyState is the watcher-facing activity signal from a Herdr agent.
type BusyState string

const (
	BusyWorking BusyState = "busy"
	BusyIdle    BusyState = "idle"
	BusyUnknown BusyState = "unknown"
)

// SubmitState is the confirmation result after attempting a literal submit.
type SubmitState string

const (
	SubmitWorking SubmitState = "working"
	SubmitBlocked SubmitState = "blocked"
	SubmitIdle    SubmitState = "idle"
	SubmitPending SubmitState = "pending"
	SubmitUnknown SubmitState = "unknown"
)

// AgentDetail is the exact native identity and state reported for one agent.
// Callers that need a particular harness must validate Agent instead of
// inferring it from task metadata.
type AgentDetail struct {
	Agent  string
	Status string
	// StateChangeSeq and Revision are Herdr's own monotonic counters for the
	// agent. They are what proves a submitted prompt was accepted: pane text
	// shows only what a harness chose to render, and a harness that collapses
	// a paste into a placeholder renders nothing to match against.
	StateChangeSeq int64
	Revision       int64
}

// PromptAccepted reports whether agent state moved between two reads: either
// monotonic counter advanced, or the agent left an input-waiting state to
// work.
//
// How much that proves depends on where the agent started. For one that was
// waiting on input, the movement is the accepted prompt, because nothing else
// moves an idle agent. For one that was already working it is weaker: a
// working agent advances state_change_seq and revision from its own turn
// output whether or not the prompt landed, and the status arm cannot help
// because it was already working. Against such an agent this is a liveness
// check, and the delivery guarantee rests on `herdr agent prompt` having
// returned success. Herdr publishes no per-prompt acceptance signal to make a
// stronger claim from.
//
// Both counters are checked because harnesses move different ones. A kimi
// agent given a prompt advanced revision while state_change_seq did not move
// and the status never reached working, so a check pinned on state_change_seq
// alone reads a delivered prompt as undelivered. Status alone is not enough
// either: an agent already working when the prompt arrived would read as
// accepting it.
func PromptAccepted(before, after AgentDetail) bool {
	if after.StateChangeSeq > before.StateChangeSeq || after.Revision > before.Revision {
		return true
	}
	return after.Status == AgentWorking && before.Status != AgentWorking
}

// Native agent_status values reported by `herdr agent list`. AgentWorking means
// the agent is actively processing a turn; AgentDone means the turn ended and
// the agent is waiting on input (finished or blocked); AgentIdle means the
// agent is registered and interactive between turns; AgentBlocked means the
// agent is parked on a permission or approval dialog awaiting human input.
const (
	AgentWorking = "working"
	AgentIdle    = "idle"
	AgentDone    = "done"
	AgentBlocked = "blocked"
	// AgentUnknown means the agent is registered but its activity is
	// transiently indeterminate (for example right after registration).
	AgentUnknown = "unknown"
)

// AgentRecord is one entry from `herdr agent list` (socket API, JSON): the
// native per-pane agent state Herdr's own UI colors from, including the
// revision and state_change_seq liveness counters the stall detector consumes.
// Both claude and pi advance state_change_seq; claude also advances revision
// while pi's revision stays static.
type AgentRecord struct {
	Agent            string `json:"agent"`
	Status           string `json:"agent_status"`
	InteractiveReady bool   `json:"interactive_ready"`
	Revision         int64  `json:"revision"`
	StateChangeSeq   int64  `json:"state_change_seq"`
	PaneID           string `json:"pane_id"`
	TabID            string `json:"tab_id"`
	WorkspaceID      string `json:"workspace_id"`
	Name             string `json:"name"`
}

// CommandError preserves failed Herdr operation context without conflating a
// normal tool exit code with a successfully interpreted business response.
type CommandError struct {
	Operation string
	Target    Target
	Stderr    string
	ExitCode  int
	Err       error
}

func (e *CommandError) Error() string {
	where := ""
	if e.Target.Session != "" || e.Target.Pane != "" {
		where = fmt.Sprintf(" for %s", e.Target.String())
	}
	if e.Err != nil {
		return fmt.Sprintf("herdr: %s%s: %v", e.Operation, where, e.Err)
	}
	if e.Stderr != "" {
		return fmt.Sprintf("herdr: %s%s exited with code %d: %s", e.Operation, where, e.ExitCode, e.Stderr)
	}
	return fmt.Sprintf("herdr: %s%s exited with code %d", e.Operation, where, e.ExitCode)
}

// Unwrap exposes a process-start or context error when one caused the failure.
func (e *CommandError) Unwrap() error {
	return e.Err
}
