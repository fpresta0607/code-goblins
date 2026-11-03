package train

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/githubapi"
)

// githubActions is the GitHub App that runs workflows. A workflow's own
// warnings are on its checks and no other app's, so the read asks for those
// alone.
const githubActions = 15368

// A workflow that gives a failed test a second try names each test that
// passed only then as a warning with this title on one of its checks, and
// ends the warning with these words when it says what happened in full. The
// go workflow of this repository does (AGENTS.md, What CI runs).
const (
	failedOnceTitle = "Failed once"
	failedOnceEnd   = " failed once and passed on the second try"
)

// failedOnceWithin is how long one read of the tests that failed once may
// take.
const failedOnceWithin = 30 * time.Second

// FailedOnce names the tests that failed once and passed on their second try
// in the workflow runs of commit sha of the GitHub repository, given as
// owner/name, each as its workflow named it: job, suite and test. run narrows
// the read to one workflow run, and 0 takes every run of the commit. A test
// that passes on its second try leaves its run green, so whoever reports the
// run without naming it lets a failure by chance pass unseen: the
// supervisor's ci_finished wakes and a merge train's own message both ask.
//
// It is one call, for the commit's checks and their warnings together. A
// read that finds no workflow check at all is an error, never an answer of
// none: a read that stopped seeing would otherwise say that nothing failed.
func FailedOnce(ctx context.Context, github *githubapi.Client, repository, sha string, run int64) ([]string, error) {
	owner, name, _ := strings.Cut(repository, "/")
	query := fmt.Sprintf("query{repository(owner:%q,name:%q){object(oid:%q){... on Commit{checkSuites(first:10,filterBy:{appId:%d}){nodes{workflowRun{databaseId} checkRuns(first:50){nodes{annotations(first:20){nodes{title message}}}}}}}}}}", owner, name, sha, githubActions)
	probe, cancel := context.WithTimeout(ctx, failedOnceWithin)
	defer cancel()
	var data struct {
		Repository struct {
			Object struct {
				CheckSuites struct {
					Nodes []struct {
						WorkflowRun *struct {
							DatabaseID int64 `json:"databaseId"`
						} `json:"workflowRun"`
						CheckRuns struct {
							Nodes []struct {
								Annotations struct {
									Nodes []struct {
										Title   string `json:"title"`
										Message string `json:"message"`
									} `json:"nodes"`
								} `json:"annotations"`
							} `json:"nodes"`
						} `json:"checkRuns"`
					} `json:"nodes"`
				} `json:"checkSuites"`
			} `json:"object"`
		} `json:"repository"`
	}
	// GitHub's own refusal of the query comes back in its words, first of
	// all.
	if err := github.GraphQL(probe, query, nil, &data); err != nil {
		var refused *githubapi.GraphQLError
		if errors.As(err, &refused) {
			return nil, fmt.Errorf("GitHub answered %s", clipped(refused.Messages[0]))
		}
		return nil, err
	}
	var names []string
	checks := 0
	for _, suite := range data.Repository.Object.CheckSuites.Nodes {
		if run != 0 && (suite.WorkflowRun == nil || suite.WorkflowRun.DatabaseID != run) {
			continue
		}
		for _, check := range suite.CheckRuns.Nodes {
			checks++
			for _, warning := range check.Annotations.Nodes {
				name := strings.TrimSuffix(strings.TrimSpace(warning.Message), failedOnceEnd)
				if warning.Title == failedOnceTitle && name != "" && !slices.Contains(names, name) {
					names = append(names, name)
				}
			}
		}
	}
	if checks == 0 {
		return nil, fmt.Errorf("GitHub listed no workflow check for %.7s", sha)
	}
	return names, nil
}

// clipped is text short enough to sit in a message.
func clipped(text string) string {
	if len(text) > 200 {
		return text[:200] + "..."
	}
	return text
}
