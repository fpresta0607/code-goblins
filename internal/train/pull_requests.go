package train

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/githubapi"
)

// pullRequestFields selects what Riders, a train and the supervisor read of a
// pull request in GitHub's GraphQL API: the fields PullRequest holds, and the
// check rollup of its head as gh's own query asks for it.
const pullRequestFields = `number url title headRefName headRefOid baseRefName isDraft isCrossRepository mergeable reviewDecision author{login} labels(first:100){nodes{name}} commits(last:1){nodes{commit{statusCheckRollup{contexts(first:100){pageInfo{hasNextPage} nodes{__typename ...on CheckRun{id databaseId name status conclusion startedAt completedAt detailsUrl checkSuite{id databaseId app{databaseId} commit{oid} repository{nameWithOwner} workflowRun{id databaseId runAttempt event file{path repositoryName} workflow{id databaseId name}}}} ...on StatusContext{context state targetUrl createdAt}}}}}}}`

const listPullRequestsQuery = `query($owner:String!,$name:String!,$base:String,$head:String){repository(owner:$owner,name:$name){pullRequests(states:OPEN,baseRefName:$base,headRefName:$head,first:100,orderBy:{field:CREATED_AT,direction:DESC}){nodes{` + pullRequestFields + `}}}}`

const viewPullRequestQuery = `query($owner:String!,$name:String!,$number:Int!){repository(owner:$owner,name:$name){pullRequest(number:$number){state mergeStateStatus ` + pullRequestFields + `}}}`

// GraphPullRequest is a pull request as GitHub's GraphQL API gives the
// fields pullRequestFields selects.
type GraphPullRequest struct {
	Number            int    `json:"number"`
	URL               string `json:"url"`
	Title             string `json:"title"`
	State             string `json:"state"`
	HeadRefName       string `json:"headRefName"`
	HeadRefOid        string `json:"headRefOid"`
	BaseRefName       string `json:"baseRefName"`
	IsDraft           bool   `json:"isDraft"`
	IsCrossRepository bool   `json:"isCrossRepository"`
	Mergeable         string `json:"mergeable"`
	MergeStateStatus  string `json:"mergeStateStatus"`
	ReviewDecision    string `json:"reviewDecision"`
	Author            struct {
		Login string `json:"login"`
	} `json:"author"`
	Labels struct {
		Nodes []Label `json:"nodes"`
	} `json:"labels"`
	Commits struct {
		Nodes []struct {
			Commit struct {
				StatusCheckRollup *struct {
					Contexts struct {
						Nodes    []GraphCheck `json:"nodes"`
						PageInfo *struct {
							HasNextPage *bool `json:"hasNextPage"`
						} `json:"pageInfo"`
					} `json:"contexts"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
}

// GraphCheck is one entry of a head's check rollup as GitHub's GraphQL API
// gives it: a check run, with a status and a conclusion, or a commit
// status, with a state.
type GraphCheck struct {
	NodeID      string `json:"id"`
	DatabaseID  int64  `json:"databaseId"`
	Kind        string `json:"__typename"`
	Name        string `json:"name"`
	Context     string `json:"context"`
	Status      string `json:"status"`
	Conclusion  string `json:"conclusion"`
	State       string `json:"state"`
	StartedAt   string `json:"startedAt"`
	CompletedAt string `json:"completedAt"`
	CreatedAt   string `json:"createdAt"`
	DetailsURL  string `json:"detailsUrl"`
	TargetURL   string `json:"targetUrl"`
	CheckSuite  struct {
		NodeID     string `json:"id"`
		DatabaseID int64  `json:"databaseId"`
		App        struct {
			DatabaseID int64 `json:"databaseId"`
		} `json:"app"`
		Commit struct {
			OID string `json:"oid"`
		} `json:"commit"`
		Repository struct {
			NameWithOwner string `json:"nameWithOwner"`
		} `json:"repository"`
		WorkflowRun *struct {
			NodeID     string `json:"id"`
			DatabaseID int64  `json:"databaseId"`
			RunAttempt int    `json:"runAttempt"`
			Event      string `json:"event"`
			File       *struct {
				Path           string `json:"path"`
				RepositoryName string `json:"repositoryName"`
			} `json:"file"`
			Workflow struct {
				NodeID     string `json:"id"`
				DatabaseID int64  `json:"databaseId"`
				Name       string `json:"name"`
			} `json:"workflow"`
		} `json:"workflowRun"`
	} `json:"checkSuite"`
}

// Check is the entry as gh's check rollup gave it, which a train's record
// keeps: a commit status starts when it was set.
func (c GraphCheck) Check() Check {
	check := Check{Kind: c.Kind, Name: c.Name, Context: c.Context, Status: c.Status, Conclusion: c.Conclusion, State: c.State, DetailsURL: c.DetailsURL, TargetURL: c.TargetURL, StartedAt: c.StartedAt, CompletedAt: c.CompletedAt}
	check.WorkflowName = c.WorkflowName()
	// Keep ordinary source checks byte-equivalent to their saved proof.
	if c.Kind == "CheckRun" && c.Name == nativeAttestationName {
		check.DatabaseID, check.NodeID = c.DatabaseID, c.NodeID
		check.SuiteID, check.SuiteNodeID, check.AppID = c.CheckSuite.DatabaseID, c.CheckSuite.NodeID, c.CheckSuite.App.DatabaseID
		check.Repository, check.HeadSHA = c.CheckSuite.Repository.NameWithOwner, c.CheckSuite.Commit.OID
		if run := c.CheckSuite.WorkflowRun; run != nil {
			check.RunID, check.RunNodeID, check.RunAttempt, check.Event = run.DatabaseID, run.NodeID, run.RunAttempt, run.Event
			check.WorkflowID, check.WorkflowNodeID = run.Workflow.DatabaseID, run.Workflow.NodeID
			if run.File != nil {
				check.WorkflowPath, check.WorkflowRepository = run.File.Path, run.File.RepositoryName
			}
		}
	}
	if c.Kind == "StatusContext" {
		check.StartedAt = c.CreatedAt
	}
	return check
}

// WorkflowName names the GitHub Actions workflow a check run belongs to, and
// is empty for another app's check, such as GitGuardian's, and for a commit
// status.
func (c GraphCheck) WorkflowName() string {
	if c.CheckSuite.WorkflowRun == nil {
		return ""
	}
	return c.CheckSuite.WorkflowRun.Workflow.Name
}

// Rollup is the check rollup of the pull request's head, every entry, the
// draft job's check too.
func (p GraphPullRequest) Rollup() []GraphCheck {
	var rollup []GraphCheck
	for _, node := range p.Commits.Nodes {
		if node.Commit.StatusCheckRollup != nil {
			rollup = append(rollup, node.Commit.StatusCheckRollup.Contexts.Nodes...)
		}
	}
	return rollup
}

func (p GraphPullRequest) completeChecks() bool {
	for _, node := range p.Commits.Nodes {
		if rollup := node.Commit.StatusCheckRollup; rollup != nil {
			page := rollup.Contexts.PageInfo
			if page == nil || page.HasNextPage == nil || *page.HasNextPage {
				return false
			}
		}
	}
	return true
}

// Checks is the rollup less the workflows' draft job's check, as Checks
// reads gh's.
func (p GraphPullRequest) Checks() Checks {
	checks := Checks{}
	for _, entry := range p.Rollup() {
		checks = append(checks, entry.Check())
	}
	return withoutDraftState(checks)
}

// PullRequest is the pull request as Riders reads it.
func (p GraphPullRequest) PullRequest() PullRequest {
	pr := PullRequest{Number: p.Number, URL: p.URL, Title: p.Title, HeadRefName: p.HeadRefName, HeadRefOid: p.HeadRefOid, BaseRefName: p.BaseRefName, IsDraft: p.IsDraft, IsCrossRepository: p.IsCrossRepository, Mergeable: p.Mergeable, ReviewDecision: p.ReviewDecision, Labels: p.Labels.Nodes, Checks: p.Checks(), IsCheckRollupIncomplete: !p.completeChecks()}
	pr.Author.Login = p.Author.Login
	return pr
}

// ListPullRequests lists the first 100 open pull requests of repository,
// owner/name, newest first, as gh pr list does: those into base and from
// head, each only when it is not empty.
func ListPullRequests(ctx context.Context, github *githubapi.Client, repository, base, head string) ([]GraphPullRequest, error) {
	owner, name, isFull := strings.Cut(repository, "/")
	if !isFull || owner == "" || name == "" {
		return nil, fmt.Errorf("%q is not an owner/name repository", repository)
	}
	variables := map[string]any{"owner": owner, "name": name}
	if base != "" {
		variables["base"] = base
	}
	if head != "" {
		variables["head"] = head
	}
	var data struct {
		Repository *struct {
			PullRequests *struct {
				Nodes []GraphPullRequest `json:"nodes"`
			} `json:"pullRequests"`
		} `json:"repository"`
	}
	if err := github.GraphQL(ctx, listPullRequestsQuery, variables, &data); err != nil {
		return nil, err
	}
	if data.Repository == nil || data.Repository.PullRequests == nil {
		return nil, fmt.Errorf("GitHub listed no open pull requests of %s", repository)
	}
	return data.Repository.PullRequests.Nodes, nil
}

// viewPullRequest reads the pull request at url, a github.com pull request's
// address, with its state and merge readiness.
func viewPullRequest(ctx context.Context, github *githubapi.Client, url string) (GraphPullRequest, error) {
	parts := strings.Split(strings.TrimPrefix(url, "https://github.com/"), "/")
	number, err := strconv.Atoi(parts[len(parts)-1])
	if !strings.HasPrefix(url, "https://github.com/") || len(parts) != 4 || parts[0] == "" || parts[1] == "" || parts[2] != "pull" || err != nil {
		return GraphPullRequest{}, fmt.Errorf("%s is not a github.com pull request's address", url)
	}
	var data struct {
		Repository *struct {
			PullRequest *GraphPullRequest `json:"pullRequest"`
		} `json:"repository"`
	}
	if err := github.GraphQL(ctx, viewPullRequestQuery, map[string]any{"owner": parts[0], "name": parts[1], "number": number}, &data); err != nil {
		return GraphPullRequest{}, err
	}
	if data.Repository == nil || data.Repository.PullRequest == nil {
		return GraphPullRequest{}, fmt.Errorf("GitHub gave no pull request at %s", url)
	}
	return *data.Repository.PullRequest, nil
}

// withoutDraftState is rollup less the workflows' draft job's check, which
// reads whether the pull request is a draft and tests nothing, whatever it
// concluded.
func withoutDraftState(rollup Checks) Checks {
	return slices.DeleteFunc(rollup, func(check Check) bool { return check.Kind == "CheckRun" && check.Name == DraftStateCheck })
}
