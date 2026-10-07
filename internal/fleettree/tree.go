// Package fleettree reads what each goblin has running under it: the
// sub-agents, background shells and monitors its harness records, the
// processes its harness started, grouped into jobs with their memory, and its
// gate run. It reads only what the harness and the machine already keep, and
// writes nothing to them. The board shows the tree, and the monitor's stale
// rules read their progress evidence from it, so one reader decides both.
package fleettree

import (
	"time"
)

// Kind is what a child of a goblin is.
type Kind string

const (
	// KindSubagent is an agent the goblin's harness started for it: a
	// Claude Code sub-agent or a Codex child agent.
	KindSubagent Kind = "subagent"
	// KindShell is a command the goblin left running in the background.
	KindShell Kind = "shell"
	// KindMonitor is a watch the goblin left running that reports events.
	KindMonitor Kind = "monitor"
	// KindProcess is a job of processes the goblin's harness started.
	KindProcess Kind = "process"
	// KindGate is the goblin's no-mistakes run.
	KindGate Kind = "gate"
)

// State is where a child stands.
type State string

const (
	Working State = "working"
	// Waiting is a child alive but not moving: an idle dev server, or a gate
	// waiting on a decision.
	Waiting State = "waiting"
	Done    State = "done"
	Failed  State = "failed"
	// Silent is a working child whose last activity is older than
	// SilentAfter.
	Silent State = "silent"
)

// Group is what a job of processes is doing, read from its programs, its
// command lines and its listening ports.
type Group string

const (
	GroupDevServer Group = "dev-server"
	GroupTest      Group = "test"
	GroupBuild     Group = "build"
	GroupBrowser   Group = "browser"
	GroupOther     Group = "other"
)

// SilentAfter is how long a working child may go without activity before it
// shows as silent: the monitor's stall interval, the mark at which it stops
// reading a quiet goblin as moving.
const SilentAfter = 10 * time.Minute

// Node is one child of a goblin.
type Node struct {
	// ID is stable for the child's life: its harness's own id for it, or a
	// job's first process id and start.
	ID   string `json:"id"`
	Kind Kind   `json:"kind"`
	// Group is what a process job, or the processes a background shell
	// runs, is doing.
	Group Group `json:"group,omitempty"`
	// Parent is the id of the child this one belongs to, for a child agent's
	// own child; empty for the goblin's own.
	Parent string `json:"parent,omitempty"`
	// Label is what the child is doing, short: its description, or what a
	// job runs.
	Label string `json:"label"`
	// Detail says more: an agent's type, a job's programs, a gate's step.
	Detail string `json:"detail,omitempty"`
	State  State  `json:"state"`
	// Started is when the child started, LastActivity when it last showed
	// any (a transcript entry, output, an event, processor use), and
	// Finished when it ended.
	Started      time.Time `json:"started"`
	LastActivity time.Time `json:"last_activity"`
	Finished     time.Time `json:"finished,omitzero"`
	// LastLine is the child's last line of output or of its own record.
	LastLine string `json:"last_line,omitempty"`
	// Memory is the private memory of the child's processes, in bytes; zero
	// for a child whose processes are not its own.
	Memory uint64 `json:"memory,omitempty"`
	// SourceUpdatedAt is when the record this node was read from last
	// changed, and FetchedAt when it was read.
	SourceUpdatedAt time.Time `json:"source_updated_at"`
	FetchedAt       time.Time `json:"fetched_at"`

	// cpu is the processor time a job's processes have used, which the
	// monitor judges progress by; process names it the way a wake does.
	cpu     time.Duration
	process string
}

// Tree is one goblin with what runs under it.
type Tree struct {
	TaskID     string `json:"task_id"`
	Generation string `json:"generation"`
	Harness    string `json:"harness"`
	// Memory is the private memory of the goblin's harness and every
	// process under it, and OwnMemory the harness's own.
	Memory    uint64 `json:"memory"`
	OwnMemory uint64 `json:"own_memory"`
	// ConversationAt is when the goblin's own conversation was last
	// written, zero when it was not found.
	ConversationAt time.Time `json:"conversation_at,omitzero"`
	Children       []Node    `json:"children"`
	// Unread names evidence that could not be read, so a tree that shows
	// nothing is never mistaken for a goblin running nothing.
	Unread          []string  `json:"unread,omitempty"`
	SourceUpdatedAt time.Time `json:"source_updated_at"`
	FetchedAt       time.Time `json:"fetched_at"`
}

// Working says a child of the goblin is working: the goblin counts as
// working while any child does.
func (t Tree) Working() bool {
	for _, child := range t.Children {
		if child.State == Working {
			return true
		}
	}
	return false
}

// ActivityAt is the latest sign of work the goblin's own records show: its
// conversation written, any agent, shell or monitor under it active, or its
// gate's step at work. A process job's activity is its processor use, which
// Jobs carries instead, and a gate waiting on a decision is not at work.
func (t Tree) ActivityAt() time.Time {
	latest := t.ConversationAt
	for _, child := range t.Children {
		if child.Kind != KindProcess && (child.Kind != KindGate || child.State == Working || child.State == Silent) && child.LastActivity.After(latest) {
			latest = child.LastActivity
		}
	}
	return latest
}

// Jobs names the goblin's running jobs of processes the way a wake names
// them, "name (pid N)", with the processor time they have used, and the
// agents, shells, monitors and gate step still at work under it, which hold
// the goblin's turn as its processes do.
func (t Tree) Jobs() ([]string, time.Duration) {
	var names []string
	var used time.Duration
	for _, child := range t.Children {
		switch {
		case child.process != "":
			names = append(names, child.process)
			used += child.cpu
		case child.Kind != KindProcess && (child.State == Working || child.State == Silent):
			names = append(names, string(child.Kind)+" "+quote(child.Label))
		}
	}
	return names, used
}

func quote(label string) string {
	return "\"" + label + "\""
}

// settle applies the rules every working child shares at now: one quiet
// for SilentAfter is silent.
func settle(node *Node, now time.Time) {
	if node.LastActivity.IsZero() {
		node.LastActivity = node.Started
	}
	if node.State == Working && !node.LastActivity.IsZero() && now.Sub(node.LastActivity) >= SilentAfter {
		node.State = Silent
	}
}
