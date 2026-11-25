package handoff

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// commands runs git in the task's worktree and gh against its repository.
type commands struct {
	runner     execx.Runner
	worktree   string
	repository string
}

func (c commands) run(ctx context.Context, name string, args ...string) (string, error) {
	result, err := c.runner.Run(ctx, execx.Request{Name: name, Args: args})
	if err != nil {
		return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("%s %s: exit %d: %s", name, strings.Join(args, " "), result.ExitCode, strings.TrimSpace(string(result.Stderr)))
	}
	return strings.TrimSpace(string(result.Stdout)), nil
}

func (c commands) git(ctx context.Context, args ...string) (string, error) {
	return c.run(ctx, "git", append([]string{"-C", c.worktree}, args...)...)
}

func (c commands) isClean(ctx context.Context) (bool, error) {
	status, err := c.git(ctx, "status", "--porcelain=v1", "--untracked-files=all")
	return status == "", err
}

func (c commands) head(ctx context.Context) (string, error) {
	return c.git(ctx, "rev-parse", "HEAD")
}

func (c commands) branch(ctx context.Context) (string, error) {
	branch, err := c.git(ctx, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return "", fmt.Errorf("the worktree is on no branch: %w", err)
	}
	return branch, nil
}

func (c commands) push(ctx context.Context, branch string) error {
	_, err := c.git(ctx, "push", "origin", "HEAD:refs/heads/"+branch)
	return err
}

// commitsSince are the commits on HEAD that the default branch lacks, one
// "<short sha> <subject>" each, oldest first.
func (c commands) commitsSince(ctx context.Context, defaultBranch string) ([]string, error) {
	if _, err := c.git(ctx, "fetch", "--quiet", "origin", "refs/heads/"+defaultBranch+":refs/remotes/origin/"+defaultBranch); err != nil {
		return nil, err
	}
	listed, err := c.git(ctx, "log", "--reverse", "--format=%h %s", "refs/remotes/origin/"+defaultBranch+"..HEAD")
	if err != nil || listed == "" {
		return nil, err
	}
	return strings.Split(listed, "\n"), nil
}

func (c commands) gh(ctx context.Context, args ...string) (string, error) {
	return c.run(ctx, "gh", args...)
}

func (c commands) defaultBranch(ctx context.Context) (string, error) {
	return c.gh(ctx, "repo", "view", c.repository, "--json", "defaultBranchRef", "--jq", ".defaultBranchRef.name")
}

// pullRequest is an open pull request as gh lists it.
type pullRequest struct {
	URL               string       `json:"url"`
	State             string       `json:"state"`
	IsDraft           bool         `json:"isDraft"`
	Body              string       `json:"body"`
	HeadRefName       string       `json:"headRefName"`
	StatusCheckRollup []checkState `json:"statusCheckRollup"`
}

// checkState is one check of a pull request's head: a check run has a
// status, a commit status a state.
type checkState struct {
	Status string `json:"status"`
	State  string `json:"state"`
}

// isCIRunning says a check of the pull request's head has not finished.
func (pr pullRequest) isCIRunning() bool {
	for _, check := range pr.StatusCheckRollup {
		if check.Status != "" && check.Status != "COMPLETED" || check.State == "PENDING" || check.State == "EXPECTED" {
			return true
		}
	}
	return false
}

const pullRequestFields = "url,state,isDraft,body,headRefName,statusCheckRollup"

func (c commands) pullRequests(ctx context.Context, side, branch string) ([]pullRequest, error) {
	listed, err := c.gh(ctx, "pr", "list", "--repo", c.repository, side, branch, "--state", "open", "--json", pullRequestFields)
	if err != nil {
		return nil, err
	}
	var prs []pullRequest
	if err := json.Unmarshal([]byte(listed), &prs); err != nil {
		return nil, fmt.Errorf("read the open pull requests with %s %s: %w", side, branch, err)
	}
	return prs, nil
}

// goblinPullRequest is the branch's open pull request, if it has one.
func (c commands) goblinPullRequest(ctx context.Context, branch string) (*pullRequest, error) {
	prs, err := c.pullRequests(ctx, "--head", branch)
	if err != nil || len(prs) == 0 {
		return nil, err
	}
	if len(prs) > 1 {
		return nil, fmt.Errorf("%s has %d open pull requests, so which one carries the packet is unknown", branch, len(prs))
	}
	return &prs[0], nil
}

func (c commands) pullRequest(ctx context.Context, url string) (pullRequest, error) {
	viewed, err := c.gh(ctx, "pr", "view", url, "--json", pullRequestFields)
	if err != nil {
		return pullRequest{}, err
	}
	var pr pullRequest
	if err := json.Unmarshal([]byte(viewed), &pr); err != nil {
		return pullRequest{}, fmt.Errorf("read %s: %w", url, err)
	}
	return pr, nil
}

// withBodyFile runs run with the path of a file holding body, which gh
// reads with --body-file and git commit with --file, since the runner hands a
// command no input.
func withBodyFile(body string, run func(path string) error) error {
	file, err := os.CreateTemp("", "goblins-handoff-*.md")
	if err != nil {
		return err
	}
	path := file.Name()
	_, writeErr := file.WriteString(body)
	err = errors.Join(writeErr, file.Close())
	if err == nil {
		err = run(path)
	}
	return errors.Join(err, os.Remove(path))
}

func (c commands) createDraft(ctx context.Context, base, branch, title, body string) (string, error) {
	var url string
	err := withBodyFile(body, func(path string) error {
		created, err := c.gh(ctx, "pr", "create", "--repo", c.repository, "--draft", "--base", base, "--head", branch, "--title", title, "--body-file", path)
		lines := strings.Split(created, "\n")
		url = strings.TrimSpace(lines[len(lines)-1])
		return err
	})
	if err == nil && !strings.HasPrefix(url, "https://github.com/") {
		err = fmt.Errorf("gh pr create answered %q, not a pull request's address", url)
	}
	return url, err
}

func (c commands) editBody(ctx context.Context, url, body string) error {
	return withBodyFile(body, func(path string) error {
		_, err := c.gh(ctx, "pr", "edit", url, "--body-file", path)
		return err
	})
}

func (c commands) closePullRequest(ctx context.Context, url string) error {
	_, err := c.gh(ctx, "pr", "close", url)
	return err
}
