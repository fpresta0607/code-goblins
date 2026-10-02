// Package tickets reports what other people have in flight in a GitHub
// repository, so the CFO never starts work someone else already has under way
// without knowing it: open issues, open and draft pull requests with the files
// they change, branches pushed recently by anyone but the Overlord, and where
// any of it overlaps the area a brief names.
//
// It also keeps a ticket, a GitHub issue, for each fleet task in such a
// repository, so teammates see what the fleet has under way: see Apply.
package tickets

import "time"

// Actor is who did something on GitHub. Login is empty for a commit whose
// author email links to no account; Name is then the git author name.
type Actor struct {
	Login     string `json:"login,omitempty"`
	Name      string `json:"name,omitempty"`
	AvatarURL string `json:"avatar_url,omitempty"`
	// IsBot is GitHub's own typing of the account as a bot. Bot accounts
	// GitHub types as users are recognised by name in isBot.
	IsBot bool `json:"-"`
}

// Issue is one open issue.
type Issue struct {
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	URL       string    `json:"url"`
	Author    Actor     `json:"author"`
	Assignees []string  `json:"assignees"`
	Labels    []string  `json:"labels"`
	CreatedAt time.Time `json:"created_at"`
	// Body is read for overlap matching and never printed: the report names
	// an issue, it does not republish it.
	Body string `json:"-"`
}

// PullRequest is one open or draft pull request and every file it changes.
type PullRequest struct {
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	URL       string    `json:"url"`
	Author    Actor     `json:"author"`
	IsDraft   bool      `json:"draft"`
	HeadRef   string    `json:"head_ref"`
	FromFork  bool      `json:"from_fork"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Files     []string  `json:"files"`
}

// Branch is one branch head. Files is read only for a branch the report
// lists that no open pull request heads; PullRequest names the one that does.
type Branch struct {
	Name        string    `json:"name"`
	Head        string    `json:"head"`
	Author      Actor     `json:"author"`
	CommittedAt time.Time `json:"committed_at"`
	PullRequest int       `json:"pull_request,omitempty"`
	Files       []string  `json:"files,omitempty"`
}

// Event kinds: the dated acts that show who works in a repository.
const (
	EventCommit      = "commit"
	EventIssue       = "issue"
	EventPullRequest = "pull_request"
)

// Event is one dated act by someone: a commit on the default branch, or an
// issue or pull request opened, whatever its state now.
type Event struct {
	Kind   string
	Number int
	Author Actor
	At     time.Time
}

// Activity is what GitHub said about one repository in one read.
type Activity struct {
	Repository    string
	Viewer        Actor
	DefaultBranch string
	DefaultHead   string
	Issues        []Issue
	PullRequests  []PullRequest
	Branches      []Branch
	Events        []Event
	// Unread names what the read stopped short of, so a report never passes
	// a partial read off as the whole repository.
	Unread []string
}
