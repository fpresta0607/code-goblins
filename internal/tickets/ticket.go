package tickets

import (
	"fmt"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/harness"
)

// State is where a task's ticket stands. It follows the task's lifecycle as
// the supervisor reads it; nobody sets it by hand.
type State string

const (
	Queued     State = "queued"
	InProgress State = "in progress"
	PROpen     State = "pr open"
	Paused     State = "paused"
	Blocked    State = "blocked"
	Merged     State = "merged"
	Closed     State = "closed"
)

// openStates are the states a ticket's issue stays open in, each with its
// own label, in the order the labels are created.
var openStates = []State{Queued, InProgress, PROpen, Paused, Blocked}

// harnesses are the harnesses a goblin can run on, each with its own label.
var harnesses = []harness.Kind{harness.Claude, harness.Codex, harness.Pi, harness.Kimi}

// Reason says why a ticket is blocked or closed. It is one of these fixed
// phrases, so a question's text or an internal note can never reach a
// teammate's repository through it.
type Reason string

const (
	WaitingOnDecision Reason = "waiting on a decision"
	StoppedOnFailure  Reason = "stopped on a failure"
	StoppedByCFO      Reason = "stopped by the CFO"
	FinishedUnmerged  Reason = "finished without a merge"
	LeftTheFleet      Reason = "no longer in the fleet"
)

// PullRequestLink is the pull request a ticket links.
type PullRequestLink struct {
	Number int
	URL    string
}

// Ticket is everything a task's issue may say: its title, where it stands,
// who is on it and its links. Nothing else of the task reaches GitHub: never
// the brief's text, a path on this machine, a secret or an internal note.
type Ticket struct {
	TaskID      string
	Title       string
	State       State
	Harness     string
	PullRequest PullRequestLink
	Reason      Reason
}

// Labels are the state label while the ticket is open and the label of the
// harness its goblin runs on, once one does.
func (t Ticket) Labels() []string {
	labels := []string{}
	if t.IsOpen() {
		labels = append(labels, stateLabel(t.State))
	}
	if t.Harness != "" {
		labels = append(labels, harnessLabel(t.Harness))
	}
	return labels
}

func stateLabel(state State) string { return "cfo: " + string(state) }

func harnessLabel(name string) string { return "goblin: " + name }

// StatusLine is the ticket's state in one line, as the issue body, a claimed
// issue's status comment and a closing comment show it.
func (t Ticket) StatusLine() string {
	switch t.State {
	case Queued:
		return "Queued"
	case InProgress:
		return "In progress: " + t.onIt()
	case PROpen:
		return fmt.Sprintf("PR open: #%d", t.PullRequest.Number)
	case Paused:
		return "Paused"
	case Blocked:
		return "Blocked: " + string(t.Reason)
	case Merged:
		return fmt.Sprintf("Merged in #%d", t.PullRequest.Number)
	default:
		return "Closed: " + string(t.Reason)
	}
}

func (t Ticket) onIt() string { return "goblin " + t.TaskID + " on " + t.Harness }

// IsOpen reports whether the ticket's issue stays open.
func (t Ticket) IsOpen() bool { return t.State != Merged && t.State != Closed }

// CloseReason is GitHub's state_reason for a closed ticket: completed for a
// merge, not_planned for anything else, and empty while it is open.
func (t Ticket) CloseReason() string {
	switch t.State {
	case Merged:
		return "completed"
	case Closed:
		return "not_planned"
	default:
		return ""
	}
}

// Body is the body of an issue cfo opened for the task: its state, who is on
// it and its pull request, rewritten in place as the state changes. The
// title is the issue's own, so the body does not repeat it.
func (t Ticket) Body() string {
	var body strings.Builder
	fmt.Fprintf(&body, "**State:** %s\n", t.StatusLine())
	if t.Harness != "" {
		fmt.Fprintf(&body, "**On it:** %s\n", t.onIt())
	}
	if t.PullRequest.URL != "" {
		fmt.Fprintf(&body, "**Pull request:** %s\n", t.PullRequest.URL)
	}
	body.WriteString("\nThe Code Goblins fleet opened this ticket for its task and keeps it current; its state follows the task.\n")
	return body.String()
}
