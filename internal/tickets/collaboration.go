package tickets

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Collaboration is whether people other than the Overlord work in a
// repository, and whether only its members can read it.
type Collaboration struct {
	Repository      string
	IsCollaborative bool
	IsPrivate       bool
}

// Collaboration reads who worked in a repository in the last 30 days, by the
// same rule the report uses, in one GraphQL round: the dated acts and the
// first page of branches, with no issue or pull request pages and no file
// reads. It is what the supervisor asks before it keeps tickets in a
// repository, so it is cheap enough to ask every hour.
func (g GitHub) Collaboration(ctx context.Context, repository string, now time.Time) (Collaboration, error) {
	owner, name, ok := strings.Cut(repository, "/")
	if !ok || owner == "" || name == "" {
		return Collaboration{}, fmt.Errorf("repository %q is not owner/name", repository)
	}
	since := now.Add(-CollaborationWindow).UTC().Format(time.RFC3339)
	response, err := g.query(ctx, owner, name, since, map[string]bool{"first": true, "refs": true}, nil)
	if err != nil {
		return Collaboration{}, err
	}
	activity := Activity{Repository: repository}
	if viewer := response.Data.Viewer; viewer != nil {
		activity.Viewer = Actor{Login: viewer.Login, Name: viewer.Name}
	}
	readFirstRound(&activity, response)
	repo := response.Data.Repository
	for _, node := range repo.Refs.Nodes {
		activity.Branches = append(activity.Branches, Branch{Name: node.Name, Head: node.Target.Oid, CommittedAt: node.Target.CommittedDate, Author: node.Target.Author.actor()})
	}
	return Collaboration{Repository: repository, IsCollaborative: len(contributors(activity, now)) > 0, IsPrivate: repo.IsPrivate}, nil
}
