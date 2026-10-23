package tickets

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// IssueState reads an issue by its recorded identity. Missing issues and
// pull requests are distinct from closed issues; neither proves completion.
func (g GitHub) IssueState(ctx context.Context, repository string, number int) (string, error) {
	out, err := g.api(ctx, "GET", fmt.Sprintf("repos/%s/issues/%d", repository, number))
	if err != nil {
		isGone, goneErr := g.isGone(ctx, repository, err)
		if goneErr != nil {
			return "", goneErr
		}
		if isGone {
			return "missing", nil
		}
		return "", err
	}
	var issue githubIssue
	if err := json.Unmarshal(out, &issue); err != nil {
		return "", fmt.Errorf("read issue %s#%d: %w", repository, number, err)
	}
	wantURL := fmt.Sprintf("https://github.com/%s/issues/%d", repository, number)
	if issue.PullRequest != nil {
		wantURL = fmt.Sprintf("https://github.com/%s/pull/%d", repository, number)
	}
	if issue.Number != number || !strings.EqualFold(issue.URL, wantURL) {
		return "", fmt.Errorf("issue identity does not match %s", wantURL)
	}
	if issue.PullRequest != nil {
		return "pull request", nil
	}
	if issue.State != "open" && issue.State != "closed" {
		return "", fmt.Errorf("issue %s has unreadable state %q", wantURL, issue.State)
	}
	return issue.State, nil
}
