package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/execx"
)

const (
	afkMergeURL  = "https://github.com/acme/api/pull/13"
	afkMergeBase = "9d8c7b6a5f4e3d2c1b0a9f8e7d6c5b4a39281706"
)

// afkMergeRunner scripts what an AFK merge reads from GitHub and records, in
// order, everything that happened: each gh call and each line the log took.
type afkMergeRunner struct {
	events []string
	logged []afk.Entry

	author    string
	viewer    string
	cross     bool
	behind    int
	mergeExit int
	logErr    error
}

func (r *afkMergeRunner) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	if request.Name != "gh" {
		return execx.Result{}, errors.New("unexpected command: " + request.Name)
	}
	r.events = append(r.events, "gh "+strings.Join(request.Args[:2], " "))
	switch {
	case request.Args[0] == "pr" && request.Args[1] == "view":
		author := r.author
		if author == "" {
			author = "fpresta0607"
		}
		return execx.Result{Stdout: []byte(fmt.Sprintf(`{"state":"OPEN","isDraft":false,"mergeable":"MERGEABLE","reviewDecision":"APPROVED","headRefOid":"%s","statusCheckRollup":[{"__typename":"CheckRun","name":"go","status":"COMPLETED","conclusion":"SUCCESS"}],"author":{"login":"%s"},"baseRefName":"main","isCrossRepository":%t}`, prMergeHeadOID, author, r.cross))}, nil
	case request.Args[0] == "api" && request.Args[1] == "user":
		viewer := r.viewer
		if viewer == "" {
			viewer = "fpresta0607"
		}
		return execx.Result{Stdout: []byte(viewer + "\n")}, nil
	case request.Args[0] == "api" && request.Args[1] == "repos/acme/api/compare/main..."+prMergeHeadOID:
		return execx.Result{Stdout: []byte(fmt.Sprintf(`{"behind_by":%d,"base":"%s"}`, r.behind, afkMergeBase))}, nil
	case request.Args[0] == "pr" && request.Args[1] == "merge":
		if r.mergeExit != 0 {
			return execx.Result{ExitCode: r.mergeExit, Stderr: []byte("gh: merge refused")}, nil
		}
		return execx.Result{Stdout: []byte("Merged pull request #13")}, nil
	}
	return execx.Result{}, errors.New("unexpected gh call: " + strings.Join(request.Args, " "))
}

func (r *afkMergeRunner) authority() *afkAuthority {
	return &afkAuthority{log: func(entry afk.Entry) error {
		if r.logErr != nil {
			return r.logErr
		}
		event := "log " + entry.Kind
		if entry.Outcome != "" {
			event += " " + entry.Outcome
		}
		r.events = append(r.events, event)
		r.logged = append(r.logged, entry)
		return nil
	}}
}

func (r *afkMergeRunner) merge(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	exit := runPRMerge(append([]string{afkMergeURL}, args...), &stdout, &stderr, r, r.authority())
	return exit, stdout.String(), stderr.String()
}

func (r *afkMergeRunner) merged() bool { return slices.Contains(r.events, "gh pr merge") }

// While AFK mode is on the CFO's merge word is logged with its evidence
// before anything merges, so no merge under the authority goes unlogged, and
// how the merge went is logged after it.
func TestAnAFKMergeLogsTheMergeWordWithItsEvidenceBeforeItMerges(t *testing.T) {
	// Arrange
	runner := &afkMergeRunner{}

	// Act
	exit, stdout, stderr := runner.merge("--verified", "gate run 41 passed and its test output was read")

	// Assert
	if exit != 0 || !strings.Contains(stdout, "Merged pull request #13") {
		t.Fatalf("exit=%d stdout=%q stderr=%q, want the pull request merged", exit, stdout, stderr)
	}
	decided, merging, outcome := slices.Index(runner.events, "log merge"), slices.Index(runner.events, "gh pr merge"), slices.Index(runner.events, "log merge merged")
	if decided < 0 || merging < decided || outcome < merging {
		t.Fatalf("events = %q, want the merge word logged, then the merge, then its outcome", runner.events)
	}
	word := runner.logged[0]
	if word.Kind != afk.KindMerge || word.What != afkMergeURL || word.Link != afkMergeURL || word.Outcome != "" {
		t.Errorf("the merge word = %+v, want the pull request named and linked", word)
	}
	for name, fact := range map[string]string{
		"what verified it":               "gate run 41 passed and its test output was read",
		"the head it merges":             "head " + prMergeHeadOID,
		"its green checks":               "1 check completed green",
		"that it is mergeable":           "mergeable",
		"whose pull request it is":       "by fpresta0607, the account gh is signed in as",
		"that it was tested on its base": "main's tip " + afkMergeBase + " is in the head, so the merge ref's first parent is that tip and CI on this head tested what lands",
	} {
		if !strings.Contains(word.Evidence, fact) {
			t.Errorf("the merge word's evidence does not say %s (%q): %s", name, fact, word.Evidence)
		}
	}
	if result := runner.logged[1]; result.What != afkMergeURL || result.Outcome != afk.OutcomeMerged {
		t.Errorf("the outcome = %+v, want the same pull request logged as merged", result)
	}
}

// Each of these is the Overlord's alone, or cannot be shown to meet the
// checks his authority names, so nothing merges and no merge word is logged.
func TestAnAFKMergeIsRefusedWhenItIsNotTheCFOsToGive(t *testing.T) {
	verified := []string{"--verified", "gate run 41 passed and its test output was read"}
	for _, c := range []struct {
		name    string
		runner  *afkMergeRunner
		args    []string
		refusal string
	}{
		{name: "nothing says what verified it", runner: &afkMergeRunner{}, refusal: "--verified"},
		{name: "a blank verification", runner: &afkMergeRunner{}, args: []string{"--verified", "  "}, refusal: "--verified"},
		{name: "deleting the branch", runner: &afkMergeRunner{}, args: append([]string{"--delete-branch"}, verified...), refusal: "deleting a branch stays the Overlord's alone"},
		{name: "a teammate's pull request", runner: &afkMergeRunner{author: "a-teammate"}, args: verified, refusal: "a-teammate's, and merging a teammate's pull request stays the Overlord's alone"},
		{name: "a pull request from another repository", runner: &afkMergeRunner{cross: true}, args: verified, refusal: "comes from another repository"},
		{name: "a head that does not hold its base's tip", runner: &afkMergeRunner{behind: 2}, args: verified, refusal: "2 commit(s) behind main"},
		{name: "a log that does not take the merge word", runner: &afkMergeRunner{logErr: errors.New("the supervisor is not running")}, args: verified, refusal: "nothing was merged"},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Act
			exit, _, stderr := c.runner.merge(c.args...)

			// Assert
			if exit == 0 || !strings.Contains(stderr, c.refusal) || !strings.Contains(stderr, "AFK mode is on") {
				t.Fatalf("exit=%d stderr=%q, want it refused as %q while AFK mode is on", exit, stderr, c.refusal)
			}
			if c.runner.merged() {
				t.Errorf("events = %q, want nothing merged", c.runner.events)
			}
			if len(c.runner.logged) != 0 {
				t.Errorf("logged = %+v, want no merge word", c.runner.logged)
			}
		})
	}
}

// A merge GitHub refuses after the merge word was given is logged as not
// merged, so the report never shows a merge word as a merge.
func TestAnAFKMergeGitHubRefusesIsLoggedAsNotMerged(t *testing.T) {
	// Arrange
	runner := &afkMergeRunner{mergeExit: 1}

	// Act
	exit, _, stderr := runner.merge("--verified", "verified locally: go test ./... passed, output read")

	// Assert
	if exit != 1 || !strings.Contains(stderr, "gh: merge refused") {
		t.Fatalf("exit=%d stderr=%q, want gh's refusal reported", exit, stderr)
	}
	if len(runner.logged) != 2 || runner.logged[1].Outcome == afk.OutcomeMerged || !strings.Contains(runner.logged[1].Outcome, "not merged") || !strings.Contains(runner.logged[1].Outcome, "gh: merge refused") {
		t.Errorf("logged = %+v, want the merge word and then that it did not merge, with gh's reason", runner.logged)
	}
}

// While AFK mode is off a merge is the Overlord's word, as before: nothing
// extra is read from GitHub, nothing is logged, and a branch may be deleted.
func TestAMergeWhileAFKModeIsOffReadsAndLogsNothingMore(t *testing.T) {
	// Arrange
	runner := &prMergeRunner{localOid: prMergeHeadOID}
	var stdout, stderr bytes.Buffer

	// Act
	exit := runPRMerge([]string{"https://github.com/o/r/pull/13", "--delete-branch", "--verified", "ignored while off"}, &stdout, &stderr, runner, nil)

	// Assert
	if exit != 0 || !runner.ran("gh", "api", "--method", "DELETE") {
		t.Fatalf("exit=%d stderr=%q, want it merged and its branch deleted", exit, stderr.String())
	}
	if runner.ran("gh", "api", "user") {
		t.Error("a merge while AFK mode is off asked GitHub who is signed in")
	}
}
