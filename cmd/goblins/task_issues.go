package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	projectcfg "github.com/fpresta0607/code-goblins/internal/project"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/tickets"
)

// pullRequestAddress is a pull request's address on GitHub.
var pullRequestAddress = regexp.MustCompile(`^https://github\.com/[\w.-]+/[\w.-]+/pull/\d+$`)

// namingTask is one task as the issues it names are read from: its
// project's issue repository, which a bare issue number belongs to, its
// brief, its backlog row's line, the --closes of its gate runs and its pull
// requests.
type namingTask struct {
	ID           string
	Repository   string
	Brief        string
	Row          string
	Closes       []string
	PullRequests []string
}

// issues are every issue the task names, each once, given what each of its
// pull requests names. A bare number of a task whose repository is unknown
// names no issue.
func (task namingTask) issues(pullIssues map[string][]tickets.IssueRef) []tickets.IssueRef {
	var named []tickets.IssueRef
	add := func(issues []tickets.IssueRef) {
		for _, issue := range issues {
			if issue.Repository != "" && !slices.Contains(named, issue) {
				named = append(named, issue)
			}
		}
	}
	add(tickets.BriefIssueRefs(task.Brief, task.Repository))
	add(tickets.RowIssueRefs(task.Row, task.Repository))
	add(tickets.ClosingIssueRefs(strings.Join(task.Closes, "\n"), task.Repository))
	for _, pull := range task.PullRequests {
		add(pullIssues[pull])
	}
	return named
}

// taskPullRequests are the pull requests task id is on record with, each
// once: the one goblins pr check recorded, the one its retirement recorded,
// each its done reports named and each its ticket named.
func taskPullRequests(stateDir, id string) ([]string, error) {
	var pulls []string
	add := func(pull string) {
		if pull = strings.TrimSpace(pull); pullRequestAddress.MatchString(pull) && !slices.Contains(pulls, pull) {
			pulls = append(pulls, pull)
		}
	}
	values, err := state.ReadMeta(state.TaskMetaPath(stateDir, id))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	add(values["pr"])
	outcome, err := state.ReadOutcome(stateDir, id)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	add(outcome.PR)
	lines, err := fsx.ReadLines(state.StatusPath(stateDir, id))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	for _, line := range lines {
		if _, event := state.SplitStatus(line); strings.HasPrefix(event, "done: PR ") {
			add(strings.TrimPrefix(event, "done: PR "))
		}
	}
	record, err := tickets.ReadRecord(stateDir, id)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	for _, pull := range record.PullRequestsOnRecord() {
		add(pull)
	}
	return pulls, nil
}

// taskBrief is the text of the brief at path, or of the task's brief in the
// home's data folder when path names none or none is there, and "" when the
// task has neither.
func taskBrief(h home.Home, id, path string) (string, error) {
	for _, candidate := range []string{path, filepath.Join(h.Data, id, "brief.md")} {
		if candidate == "" {
			continue
		}
		text, err := fsx.ReadFile(candidate)
		if err == nil {
			return string(text), nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
	}
	return "", nil
}

// issueRepository is the GitHub repository the issues of the project at
// checkout go on: the one its project record names, or its origin. A
// checkout with no GitHub origin has none.
func issueRepository(ctx context.Context, h home.Home, repositoryOf func(context.Context, string) (string, error), checkout string) (string, error) {
	manifest, path, err := projectcfg.LoadForCheckout(h.Data, checkout)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("read the project record %s: %w", path, err)
	}
	if manifest.IssueRepository != "" {
		return strings.ToLower(manifest.IssueRepository), nil
	}
	repository, err := repositoryOf(ctx, checkout)
	if errors.Is(err, tickets.ErrNotGitHub) {
		return "", nil
	}
	return strings.ToLower(repository), err
}

// openTaskIssues names, as owner/name#N, each issue a task names that is
// still open on GitHub: an issue its brief names, one its branch's gate runs
// were started to close and one its pull requests name. An issue or a pull
// request that cannot be read is an error, never an issue taken as closed. A
// project checkout gone leaves the task's bare issue numbers no repository,
// and a worktree no longer on a branch leaves its gate runs unread.
func openTaskIssues(h home.Home, github tickets.GitHub, gate pipeline.Reader) func(context.Context, state.TaskMeta) ([]string, error) {
	return func(ctx context.Context, meta state.TaskMeta) ([]string, error) {
		task := namingTask{ID: meta.ID}
		if _, err := os.Stat(meta.Project); err == nil {
			if task.Repository, err = issueRepository(ctx, h, github.RepositoryOf, meta.Project); err != nil {
				return nil, err
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		brief, err := taskBrief(h, meta.ID, meta.Brief)
		if err != nil {
			return nil, err
		}
		task.Brief = brief
		if task.Closes, err = gateClosingIssues(ctx, github.Commands, gate, meta); err != nil {
			return nil, err
		}
		if task.PullRequests, err = taskPullRequests(h.State, meta.ID); err != nil {
			return nil, err
		}
		pullIssues, unread, err := github.PullRequestIssues(ctx, task.PullRequests)
		if err != nil {
			return nil, err
		}
		if len(unread) > 0 {
			return nil, fmt.Errorf("GitHub shows no pull request at %s", strings.Join(unread, ", "))
		}
		var open []string
		for _, issue := range task.issues(pullIssues) {
			issueState, err := github.IssueState(ctx, issue.Repository, issue.Number)
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", issue, err)
			}
			if issueState == "open" {
				open = append(open, issue.String())
			}
		}
		return open, nil
	}
}

// gateClosingIssues are the --closes of the gate runs of the branch the
// task's worktree is on, none while it is on none or the gate has no
// database.
func gateClosingIssues(ctx context.Context, commands execx.Runner, gate pipeline.Reader, meta state.TaskMeta) ([]string, error) {
	if _, err := os.Stat(filepath.Join(gate.Root, "state.sqlite")); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if _, err := os.Stat(meta.Worktree); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	branch, err := commands.Run(ctx, execx.Request{Dir: meta.Worktree, Name: "git", Args: []string{"symbolic-ref", "--quiet", "--short", "HEAD"}})
	if err != nil || branch.ExitCode != 0 {
		return nil, nil
	}
	return gate.ClosingIssues(ctx, meta.Project, strings.TrimSpace(string(branch.Stdout)))
}
