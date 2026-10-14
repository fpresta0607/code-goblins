// Command pushreuse lets a push to the default branch take the result of a
// pull request run that already tested its exact tree on the same base,
// instead of running a workflow's jobs again.
//
// A pull request's run tests the merge of the pull request into its base, a
// commit whose tree and first parent are what the push that lands it will
// have when nothing moved the base between. A full run that passed records
// that pair under an artifact named for it, which name prints:
//
//	go run ./tools/pushreuse name <workflow file>
//
// A push run asks whether such a record names its own commit's pair, from a
// pull request run of the same workflow in this repository that ended green:
//
//	go run ./tools/pushreuse find <workflow file> >> $GITHUB_OUTPUT
//
// find writes run=<id> when it finds one and nothing otherwise, and says why
// on standard error. It exits 0 whatever it read, since a record it cannot
// read only means the push runs every job, as it would have before.
// GITHUB_REPOSITORY and GITHUB_SHA name the commit, which -sha overrides for
// a dry run against any commit.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// api reads one GitHub REST path and returns its body.
type api func(path string) ([]byte, error)

func main() {
	flags := flag.NewFlagSet("pushreuse", flag.ExitOnError)
	sha := flags.String("sha", os.Getenv("GITHUB_SHA"), "the commit to name or find a record for")
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: pushreuse name|find <workflow file> [-sha <commit>]")
		os.Exit(2)
	}
	command, workflow := os.Args[1], os.Args[2]
	_ = flags.Parse(os.Args[3:])
	repository := os.Getenv("GITHUB_REPOSITORY")
	switch command {
	case "name":
		name, err := recordName(gh, repository, workflow, *sha)
		if err != nil {
			fmt.Fprintln(os.Stderr, "pushreuse:", err)
			os.Exit(1)
		}
		fmt.Println(name)
	case "find":
		find(gh, repository, workflow, *sha, os.Stdout, os.Stderr)
	default:
		fmt.Fprintln(os.Stderr, "usage: pushreuse name|find <workflow file> [-sha <commit>]")
		os.Exit(2)
	}
}

// gh reads path through the GitHub CLI, which the runner signs in with
// GH_TOKEN, and gives up on it after a minute.
func gh(path string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	result, err := execx.OSRunner{}.Run(ctx, execx.Request{Name: "gh", Args: []string{"api", path}})
	if err != nil {
		return nil, fmt.Errorf("gh api %s: %w", path, err)
	}
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("gh api %s: exit %d: %s", path, result.ExitCode, strings.TrimSpace(string(result.Stderr)))
	}
	return result.Stdout, nil
}

// recordName is the artifact name of a passed run of workflow on the commit
// sha: the workflow's own name, then the tree and the first parent the
// commit has.
func recordName(read api, repository, workflow, sha string) (string, error) {
	if repository == "" || sha == "" {
		return "", errors.New("GITHUB_REPOSITORY and GITHUB_SHA must name the commit")
	}
	body, err := read("repos/" + repository + "/git/commits/" + sha)
	if err != nil {
		return "", err
	}
	var commit struct {
		Tree struct {
			SHA string `json:"sha"`
		} `json:"tree"`
		Parents []struct {
			SHA string `json:"sha"`
		} `json:"parents"`
	}
	if err := json.Unmarshal(body, &commit); err != nil {
		return "", fmt.Errorf("commit %s: %w", sha, err)
	}
	if commit.Tree.SHA == "" || len(commit.Parents) == 0 || commit.Parents[0].SHA == "" {
		return "", fmt.Errorf("commit %s has no tree or no parent", sha)
	}
	return strings.TrimSuffix(path.Base(workflow), path.Ext(workflow)) + "-passed-" + commit.Tree.SHA + "-" + commit.Parents[0].SHA, nil
}

// find writes run=<id> to stdout for a pull request run of workflow in this
// repository that passed on the tree and first parent the commit sha has,
// and says on stderr what it found or why it found none.
func find(read api, repository, workflow, sha string, stdout, stderr io.Writer) {
	run, why := passedRun(read, repository, workflow, sha)
	if run == 0 {
		fmt.Fprintf(stderr, "pushreuse: every job runs: %s\n", why)
		return
	}
	fmt.Fprintf(stderr, "pushreuse: %s\n", why)
	fmt.Fprintf(stdout, "run=%d\n", run)
}

type artifact struct {
	Name        string `json:"name"`
	Expired     bool   `json:"expired"`
	WorkflowRun struct {
		ID               int64 `json:"id"`
		RepositoryID     int64 `json:"repository_id"`
		HeadRepositoryID int64 `json:"head_repository_id"`
	} `json:"workflow_run"`
}

type workflowRun struct {
	Event      string `json:"event"`
	Path       string `json:"path"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	HTMLURL    string `json:"html_url"`
	Repository struct {
		ID int64 `json:"id"`
	} `json:"repository"`
	HeadRepository struct {
		ID int64 `json:"id"`
	} `json:"head_repository"`
}

// passedRun is the id of the run find looks for, or 0 and why there is none.
// A record counts only from a pull request run of the same workflow file
// whose head is in this repository and which ended green: a fork's pull
// request runs code its author chose, and could record any pair.
func passedRun(read api, repository, workflow, sha string) (int64, string) {
	name, err := recordName(read, repository, workflow, sha)
	if err != nil {
		return 0, err.Error()
	}
	body, err := read("repos/" + repository + "/actions/artifacts?per_page=100&name=" + url.QueryEscape(name))
	if err != nil {
		return 0, err.Error()
	}
	var listed struct {
		Artifacts []artifact `json:"artifacts"`
	}
	if err := json.Unmarshal(body, &listed); err != nil {
		return 0, fmt.Sprintf("the records named %s: %v", name, err)
	}
	why := "no pull request run recorded that it passed " + name
	for _, record := range listed.Artifacts {
		from := record.WorkflowRun
		switch {
		case record.Name != name:
			continue
		case record.Expired:
			why = fmt.Sprintf("run %d's record of %s expired", from.ID, name)
			continue
		case from.ID == 0 || from.RepositoryID == 0 || from.HeadRepositoryID != from.RepositoryID:
			why = fmt.Sprintf("run %d's record of %s came from another repository's head", from.ID, name)
			continue
		}
		body, err := read(fmt.Sprintf("repos/%s/actions/runs/%d", repository, from.ID))
		if err != nil {
			why = err.Error()
			continue
		}
		var run workflowRun
		if err := json.Unmarshal(body, &run); err != nil {
			why = fmt.Sprintf("run %d: %v", from.ID, err)
			continue
		}
		file, _, _ := strings.Cut(run.Path, "@")
		switch {
		case run.Event != "pull_request":
			why = fmt.Sprintf("run %d recorded %s on a %s event, not a pull request's", from.ID, name, run.Event)
		case file != ".github/workflows/"+path.Base(workflow):
			why = fmt.Sprintf("run %d recorded %s from %s, not %s", from.ID, name, run.Path, workflow)
		case run.Repository.ID != from.RepositoryID || run.HeadRepository.ID != from.RepositoryID:
			why = fmt.Sprintf("run %d's head is in another repository", from.ID)
		case run.Status != "completed" || run.Conclusion != "success":
			why = fmt.Sprintf("run %d recorded %s but ended %s %s", from.ID, name, run.Status, run.Conclusion)
		default:
			return from.ID, fmt.Sprintf("pull request run %d (%s) passed every job on tree and first parent %s, so this push runs none again", from.ID, run.HTMLURL, strings.TrimPrefix(name, strings.TrimSuffix(path.Base(workflow), path.Ext(workflow))+"-passed-"))
		}
	}
	return 0, why
}
