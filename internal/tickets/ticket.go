package tickets

import (
	"fmt"
	"regexp"
	"slices"
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
var harnesses = []harness.Kind{harness.Claude, harness.Codex, harness.Pi}

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

// Ticket is everything a task's issue may say: its title, what it changes
// and where, where it stands, who is on it and its links. Nothing else of the
// task reaches GitHub: never the brief's text beyond its Ticket section, a
// path on this machine, a secret or an internal note.
type Ticket struct {
	TaskID string
	Title  string
	// What is the Ticket section of the task's brief: the two or three
	// sentences the CFO wrote there for teammates, and the one text of a
	// brief a ticket carries.
	What string
	// Area is the repository paths the brief's work names that the
	// project's checkout has.
	Area        []string
	State       State
	Harness     string
	PullRequest PullRequestLink
	// PullRequests are every pull request of the task the ticket holds, in
	// the order they appeared, PullRequest among them.
	PullRequests []PullRequestLink
	Reason       Reason
	// Overlap is the CFO's reason for starting the task beside a teammate's
	// work, when he did; he writes it for the ticket.
	Overlap string
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

// StatusComment is the status comment of an issue the task claimed: its
// status line, and the overlap it was started beside when there was one.
func (t Ticket) StatusComment() string {
	if t.Overlap == "" {
		return t.StatusLine()
	}
	return t.StatusLine() + "\nStarted beside other work: " + t.Overlap
}

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

// Body is the body of an issue cfo opened for the task: what it changes and
// where, its state, who is on it and every pull request of the task,
// rewritten in place as the state changes. The title is the issue's own, so
// the body does not repeat it.
func (t Ticket) Body() string {
	var body strings.Builder
	if t.What != "" {
		fmt.Fprintf(&body, "**What:** %s\n", t.What)
	}
	if len(t.Area) > 0 {
		fmt.Fprintf(&body, "**Area:** `%s`\n", strings.Join(t.Area, "`, `"))
	}
	fmt.Fprintf(&body, "**State:** %s\n", t.StatusLine())
	if t.Harness != "" {
		fmt.Fprintf(&body, "**On it:** %s\n", t.onIt())
	}
	switch urls := t.pullRequestURLs(); len(urls) {
	case 0:
	case 1:
		fmt.Fprintf(&body, "**Pull request:** %s\n", urls[0])
	default:
		fmt.Fprintf(&body, "**Pull requests:** %s\n", strings.Join(urls, ", "))
	}
	if t.Overlap != "" {
		fmt.Fprintf(&body, "**Started beside other work:** %s\n", t.Overlap)
	}
	body.WriteString("\nThe Code Goblins fleet opened this ticket for its task and keeps it current; its state follows the task.\n")
	return body.String()
}

// pullRequestURLs are the links of every pull request the ticket holds, its
// current one among them, each once and in the order they appeared.
func (t Ticket) pullRequestURLs() []string {
	var urls []string
	for _, pull := range append(slices.Clone(t.PullRequests), t.PullRequest) {
		if pull.URL != "" && !slices.Contains(urls, pull.URL) {
			urls = append(urls, pull.URL)
		}
	}
	return urls
}

var pullRequestURL = regexp.MustCompile(`^https://github\.com/([\w.-]+/[\w.-]+)/pull/\d+$`)

// IsPullRequestOf reports whether url links a pull request of repository.
// GitHub does not tell a repository's names apart by case.
func IsPullRequestOf(url, repository string) bool {
	match := pullRequestURL.FindStringSubmatch(url)
	return match != nil && strings.EqualFold(match[1], repository)
}
