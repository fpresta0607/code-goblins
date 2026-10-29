package tickets

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
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
	args := []string{"api", "graphql", "-f", "query=" + closingPullsQuery, "-f", "owner=" + owner, "-f", "name=" + name, "-F", "number=" + strconv.Itoa(number)}
	result, err := g.Commands.Run(ctx, execx.Request{Name: "gh", Args: args})
	if err != nil {
		return nil, fmt.Errorf("gh api graphql: %w", err)
	}
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("read the pull requests that close %s#%d: gh api graphql exited %d: %s", repository, number, result.ExitCode, strings.TrimSpace(string(result.Stderr)))
	}
	var answer struct {
		Data struct {
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
		} `json:"data"`
	}
	if err := json.Unmarshal(result.Stdout, &answer); err != nil {
		return nil, fmt.Errorf("read the pull requests that close %s#%d: %w", repository, number, err)
	}
	if answer.Data.Repository == nil || answer.Data.Repository.Issue == nil || answer.Data.Repository.Issue.Closing == nil {
		return nil, fmt.Errorf("GitHub did not say which pull requests close %s#%d", repository, number)
	}
	var open []string
	for _, pull := range answer.Data.Repository.Issue.Closing.Nodes {
		if pull.State == "OPEN" {
			open = append(open, pull.URL)
		}
	}
	return open, nil
}
