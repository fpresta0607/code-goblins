package main

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

const (
	repository = "o/r"
	pushed     = "c0ffee"
	tree       = "7ree"
	mainBefore = "ba5e"
	recordedAs = "go-passed-7ree-ba5e"
	repoID     = 101
)

// fakeGitHub answers the reads find makes: the pushed commit, the records
// named for its pair, and each run a record names.
type fakeGitHub struct {
	commit    string
	artifacts string
	runs      map[int64]string
	failing   string
}

func (f fakeGitHub) read(path string) ([]byte, error) {
	if f.failing != "" && strings.HasPrefix(path, f.failing) {
		return nil, errors.New("gh api " + path + ": HTTP 502")
	}
	switch {
	case path == "repos/o/r/git/commits/"+pushed:
		return []byte(f.commit), nil
	case path == "repos/o/r/actions/artifacts?per_page=100&name="+recordedAs:
		return []byte(f.artifacts), nil
	}
	for id, run := range f.runs {
		if path == fmt.Sprintf("repos/o/r/actions/runs/%d", id) {
			return []byte(run), nil
		}
	}
	return nil, errors.New("gh api " + path + ": HTTP 404")
}

func record(run, head int64, isExpired bool) string {
	return fmt.Sprintf(`{"name":%q,"expired":%t,"workflow_run":{"id":%d,"repository_id":%d,"head_repository_id":%d}}`, recordedAs, isExpired, run, repoID, head)
}

func run(event, path, conclusion string, head int64) string {
	return fmt.Sprintf(`{"event":%q,"path":%q,"status":"completed","conclusion":%q,"html_url":"https://github.com/o/r/actions/runs/7","repository":{"id":%d},"head_repository":{"id":%d}}`, event, path, conclusion, repoID, head)
}

const mergeCommit = `{"sha":"c0ffee","tree":{"sha":"7ree"},"parents":[{"sha":"ba5e"},{"sha":"he4d"}]}`

// A push to main runs nothing again only when a full pull request run of the
// same workflow in this repository passed on its exact tree and first
// parent. Any other push, and any record find cannot read, runs every job:
// the default is the run as it always was.
func TestFindTakesOnlyAGreenPullRequestRunOfTheSameTreeOnTheSameMain(t *testing.T) {
	green := run("pull_request", ".github/workflows/go.yml", "success", repoID)
	for name, test := range map[string]struct {
		github  fakeGitHub
		wantRun string
		wantWhy string
	}{
		"a train's green run of the same tree on the same main": {
			fakeGitHub{commit: mergeCommit, artifacts: `{"artifacts":[` + record(7, repoID, false) + `]}`, runs: map[int64]string{7: green}},
			"run=7\n", "passed every job on tree and first parent 7ree-ba5e",
		},
		"no run recorded this tree on this main": {
			fakeGitHub{commit: mergeCommit, artifacts: `{"artifacts":[]}`},
			"", "no pull request run recorded",
		},
		"the record expired": {
			fakeGitHub{commit: mergeCommit, artifacts: `{"artifacts":[` + record(7, repoID, true) + `]}`, runs: map[int64]string{7: green}},
			"", "expired",
		},
		"a fork's pull request recorded it": {
			fakeGitHub{commit: mergeCommit, artifacts: `{"artifacts":[` + record(7, 999, false) + `]}`, runs: map[int64]string{7: green}},
			"", "another repository",
		},
		"the run says its head is a fork's": {
			fakeGitHub{commit: mergeCommit, artifacts: `{"artifacts":[` + record(7, repoID, false) + `]}`, runs: map[int64]string{7: run("pull_request", ".github/workflows/go.yml", "success", 999)}},
			"", "another repository",
		},
		"the run that recorded it ended red": {
			fakeGitHub{commit: mergeCommit, artifacts: `{"artifacts":[` + record(7, repoID, false) + `]}`, runs: map[int64]string{7: run("pull_request", ".github/workflows/go.yml", "failure", repoID)}},
			"", "ended completed failure",
		},
		"another workflow recorded it": {
			fakeGitHub{commit: mergeCommit, artifacts: `{"artifacts":[` + record(7, repoID, false) + `]}`, runs: map[int64]string{7: run("pull_request", ".github/workflows/install.yml", "success", repoID)}},
			"", "not go.yml",
		},
		"a push run recorded it": {
			fakeGitHub{commit: mergeCommit, artifacts: `{"artifacts":[` + record(7, repoID, false) + `]}`, runs: map[int64]string{7: run("push", ".github/workflows/go.yml", "success", repoID)}},
			"", "push event",
		},
		"the records cannot be listed": {
			fakeGitHub{commit: mergeCommit, failing: "repos/o/r/actions/artifacts"},
			"", "HTTP 502",
		},
		"a commit with no parent": {
			fakeGitHub{commit: `{"sha":"c0ffee","tree":{"sha":"7ree"},"parents":[]}`},
			"", "no parent",
		},
		"a red record beside a green one": {
			fakeGitHub{commit: mergeCommit, artifacts: `{"artifacts":[` + record(6, repoID, false) + `,` + record(7, repoID, false) + `]}`, runs: map[int64]string{6: run("pull_request", ".github/workflows/go.yml", "cancelled", repoID), 7: green}},
			"run=7\n", "passed every job",
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			var stdout, stderr bytes.Buffer

			// Act
			find(test.github.read, repository, "go.yml", pushed, &stdout, &stderr)

			// Assert
			if stdout.String() != test.wantRun {
				t.Errorf("find wrote %q, want %q (it said %q)", stdout.String(), test.wantRun, stderr.String())
			}
			if !strings.Contains(stderr.String(), test.wantWhy) {
				t.Errorf("find said %q, want it to say %q", stderr.String(), test.wantWhy)
			}
			if test.wantRun == "" && !strings.Contains(stderr.String(), "every job runs") {
				t.Errorf("find said %q for a push that runs every job, want it to say so", stderr.String())
			}
		})
	}
}

// A pull request run names its record by the commit it tested, the merge of
// the pull request into its base: the workflow, the tree and the base.
func TestNameIsTheWorkflowTheTreeAndTheFirstParent(t *testing.T) {
	// Act
	name, err := recordName(fakeGitHub{commit: mergeCommit}.read, repository, ".github/workflows/go.yml", pushed)

	// Assert
	if err != nil || name != recordedAs {
		t.Fatalf("name = %q (%v), want %q", name, err, recordedAs)
	}
}
