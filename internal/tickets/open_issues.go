package tickets

import (
	"context"
	"encoding/json"
	"fmt"
)

// OpenIssue is an open issue of a repository as GitHub lists it.
type OpenIssue struct {
	Number int
	Title  string
	URL    string
	Labels []string
}

// OpenIssues reads every open issue of repository, owner/name, with the pull
// requests GitHub lists among them left out, a page of a hundred at a time, up
// to maxPageRounds pages; a repository with more is an error, never a partial
// answer.
func (g GitHub) OpenIssues(ctx context.Context, repository string) ([]OpenIssue, error) {
	var open []OpenIssue
	for page := 1; page <= maxPageRounds; page++ {
		out, err := g.api(ctx, "GET", fmt.Sprintf("repos/%s/issues?state=open&per_page=100&page=%d", repository, page))
		if err != nil {
			return nil, err
		}
		var issues []struct {
			githubIssue
			Title string `json:"title"`
		}
		if err := json.Unmarshal(out, &issues); err != nil {
			return nil, fmt.Errorf("read the open issues of %s: %w", repository, err)
		}
		for _, issue := range issues {
			if issue.PullRequest != nil {
				continue
			}
			listed := OpenIssue{Number: issue.Number, Title: issue.Title, URL: issue.URL}
			for _, label := range issue.Labels {
				listed.Labels = append(listed.Labels, label.Name)
			}
			open = append(open, listed)
		}
		if len(issues) < 100 {
			return open, nil
		}
	}
	return nil, fmt.Errorf("%s has more than %d open issues, so they cannot all be read", repository, maxPageRounds*100)
}
