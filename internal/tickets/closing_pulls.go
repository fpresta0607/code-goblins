package tickets

import (
	"context"
	"fmt"
	"strings"
)

const closingPullsQuery = `query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    issue(number: $number) {
      closedByPullRequestsReferences(first: 100, includeClosedPrs: false) { nodes { url state } }
    }
  }
}`

// OpenClosingPullRequests are the addresses of the open pull requests GitHub
// links to close issue number of repository, owner/name: each whose text
// closes it with a keyword, such as Fixes #N, or that was linked to it by
// hand. GitHub lists merged ones too, which are left out. An answer that
// does not say is an error, never an empty list, since an empty list lets the
// issue be taken up beside the pull request that already does.
func (g GitHub) OpenClosingPullRequests(ctx context.Context, repository string, number int) ([]string, error) {
	owner, name, isFull := strings.Cut(repository, "/")
	if !isFull || owner == "" || name == "" {
		return nil, fmt.Errorf("%q is not an owner/name repository", repository)
	}
	var answer struct {
		Repository *struct {
			Issue *struct {
				Closing *struct {
					Nodes []struct {
						URL   string `json:"url"`
						State string `json:"state"`
					} `json:"nodes"`
				} `json:"closedByPullRequestsReferences"`
			} `json:"issue"`
		} `json:"repository"`
	}
	if err := g.API.GraphQL(ctx, closingPullsQuery, map[string]any{"owner": owner, "name": name, "number": number}, &answer); err != nil {
		return nil, fmt.Errorf("read the pull requests that close %s#%d: %w", repository, number, err)
	}
	if answer.Repository == nil || answer.Repository.Issue == nil || answer.Repository.Issue.Closing == nil {
		return nil, fmt.Errorf("GitHub did not say which pull requests close %s#%d", repository, number)
	}
	var open []string
	for _, pull := range answer.Repository.Issue.Closing.Nodes {
		if pull.State == "OPEN" {
			open = append(open, pull.URL)
		}
	}
	return open, nil
}
