package main

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/githubtest"
)

const (
	afkMergeURL  = "https://github.com/acme/api/pull/13"
	afkMergeBase = "9d8c7b6a5f4e3d2c1b0a9f8e7d6c5b4a39281706"
)

// afkMergeRunner scripts what an AFK merge reads from GitHub and records, in
// order, everything that happened: each gh call, each request of GitHub's API
// and each line the log took. GitHub's API answers the reads of a pull request
// on github.com, and a gh api started for one fails; gh answers them for a
// pull request on host, another host, and the API fails them.
type afkMergeRunner struct {
	events []string
	logged []afk.Entry

	host string

	author    string
	viewer    string
	cross     bool
	behind    int
	mergeExit int
	logErr    error
	// queue says the base requires a merge queue.
	queue bool
	// rollup is the head's check rollup, else one green check.
	rollup string
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
		rollup := r.rollup
		if rollup == "" {
			rollup = `{"__typename":"CheckRun","name":"go","status":"COMPLETED","conclusion":"SUCCESS"}`
		}
		return execx.Result{Stdout: []byte(fmt.Sprintf(`{"state":"OPEN","isDraft":false,"mergeable":"MERGEABLE","reviewDecision":"APPROVED","headRefOid":"%s","statusCheckRollup":[%s],"author":{"login":"%s"},"baseRefName":"main","isCrossRepository":%t}`, prMergeHeadOID, rollup, author, r.cross))}, nil
	case request.Args[0] == "api" && r.host == "":
		return execx.Result{}, errors.New("gh started for a read GitHub's API answers: gh " + strings.Join(request.Args, " "))
	case request.Args[0] == "api" && request.Args[1] == "graphql":
		return execx.Result{Stdout: []byte(mergeQueueAnswer(r.queue, false))}, nil
	case request.Args[0] == "api" && request.Args[1] == "user":
		return execx.Result{Stdout: []byte(r.login() + "\n")}, nil
	case request.Args[0] == "api" && request.Args[1] == afkMergeComparison:
		return execx.Result{Stdout: []byte(r.comparison())}, nil
	case request.Args[0] == "pr" && request.Args[1] == "merge":
		if r.mergeExit != 0 {
			return execx.Result{ExitCode: r.mergeExit, Stderr: []byte("gh: merge refused")}, nil
		}
		return execx.Result{Stdout: []byte("Merged pull request #13")}, nil
	}
	return execx.Result{}, errors.New("unexpected gh call: " + strings.Join(request.Args, " "))
}

// afkMergeComparison is the comparison of the head with its base main.
const afkMergeComparison = "repos/acme/api/compare/main..." + prMergeHeadOID

// ServeHTTP answers the reads of GitHub's API an AFK merge of a pull request
// on github.com makes.
func (r *afkMergeRunner) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	path := strings.TrimPrefix(request.URL.RequestURI(), "/")
	r.events = append(r.events, request.Method+" "+path)
	switch {
	case r.host != "":
		githubtest.Refuse(w, http.StatusInternalServerError, "GitHub's API was asked of a pull request on "+r.host)
	case request.Method == http.MethodPost && path == "graphql":
		_, _ = w.Write([]byte(mergeQueueAnswer(r.queue, false)))
	case request.Method == http.MethodGet && path == "user":
		githubtest.Write(w, http.StatusOK, map[string]string{"login": r.login()})
	case request.Method == http.MethodGet && path == afkMergeComparison:
		_, _ = w.Write([]byte(r.comparison()))
	default:
		githubtest.Refuse(w, http.StatusNotFound, "Not Found")
	}
}

// login is the account gh is signed in as.
func (r *afkMergeRunner) login() string { return cmp.Or(r.viewer, "fpresta0607") }

// comparison is GitHub's comparison of the head with main, in its own shape.
func (r *afkMergeRunner) comparison() string {
	return fmt.Sprintf(`{"behind_by":%d,"base_commit":{"sha":"%s"},"files":[]}`, r.behind, afkMergeBase)
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
	pullRequest := afkMergeURL
	if r.host != "" {
		pullRequest = "https://" + r.host + "/acme/api/pull/13"
	}
	exit := runPRMerge(append([]string{pullRequest}, args...), &stdout, &stderr, r, githubtest.Client(r), r.authority())
	return exit, stdout.String(), stderr.String()
}

func (r *afkMergeRunner) merged() bool { return slices.Contains(r.events, "gh pr merge") }

// While AFK mode is on the CEO's merge word is logged with its evidence
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
		"that it was tested on its base": "main's tip " + afkMergeBase + " was in the head when this was read, so the merge ref's first parent was that tip and CI on this head tested the merge of it",
	} {
		if !strings.Contains(word.Evidence, fact) {
			t.Errorf("the merge word's evidence does not say %s (%q): %s", name, fact, word.Evidence)
		}
	}
	if result := runner.logged[1]; result.What != afkMergeURL || result.Outcome != afk.OutcomeMerged {
		t.Errorf("the outcome = %+v, want the same pull request logged as merged", result)
	}
}

// A deploy the host rate limited never built the head, so the merge goes
// ahead on the checks that ran, and both the CEO and the merge word's
// evidence are told of the deploy rather than counting it green.
func TestAnAFKMergeNamesARateLimitedDeployInItsEvidence(t *testing.T) {
	// Arrange
	runner := &afkMergeRunner{rollup: `{"__typename":"CheckRun","name":"go","status":"COMPLETED","conclusion":"SUCCESS"},{"__typename":"StatusContext","context":"Vercel","startedAt":"2026-10-19T05:10:00Z","state":"FAILURE","targetUrl":"https://vercel.com/acme-projects?upgradeToPro=build-rate-limit"}`}

	// Act
	exit, stdout, stderr := runner.merge("--verified", "gate run 41 passed and its test output was read")

	// Assert
	const said = "the host rate limited the deploy of Vercel, which never built this head and counts as not run"
	if exit != 0 || !runner.merged() || !strings.Contains(stdout, said) {
		t.Fatalf("exit=%d stdout=%q stderr=%q, want it merged and the rate-limited deploy named", exit, stdout, stderr)
	}
	if evidence := runner.logged[0].Evidence; !strings.Contains(evidence, "1 check completed green; "+said) {
		t.Errorf("the merge word's evidence = %q, want one green check and the rate-limited deploy named", evidence)
	}
}

// Each of these is the Overlord's alone, or cannot be shown to meet the
// checks his authority names, so nothing merges and no merge word is logged.
func TestAnAFKMergeIsRefusedWhenItIsNotTheCEOsToGive(t *testing.T) {
	verified := []string{"--verified", "gate run 41 passed and its test output was read"}
	for _, c := range []struct {
		name    string
		runner  *afkMergeRunner
		args    []string
		refusal string
	}{
		{name: "nothing says what verified it", runner: &afkMergeRunner{}, refusal: "--verified"},
		{name: "a blank verification", runner: &afkMergeRunner{}, args: []string{"--verified", "  "}, refusal: "--verified"},
		{name: "deleting the branch", runner: &afkMergeRunner{}, args: append([]string{"--delete-branch"}, verified...), refusal: "deleting a branch through a merge stays the Overlord's alone"},
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
	exit := runPRMerge([]string{"https://github.com/o/r/pull/13", "--delete-branch", "--verified", "ignored while off"}, &stdout, &stderr, runner, githubtest.Client(runner), nil)

	// Assert
	if exit != 0 || !runner.ran("gh", "api", "--method", "DELETE") {
		t.Fatalf("exit=%d stderr=%q, want it merged and its branch deleted", exit, stderr.String())
	}
	if slices.Contains(runner.asked, "GET user") {
		t.Error("a merge while AFK mode is off asked GitHub who is signed in")
	}
}

// An AFK merge reads the account, the merge queue and the comparison of a
// pull request on github.com through GitHub's API, and starts gh for none of
// them; gh reads them for a pull request on another host, which the API
// client does not reach.
func TestAnAFKMergeReadsGitHubThroughItsAPIAndAnotherHostThroughGh(t *testing.T) {
	for _, row := range []struct {
		name  string
		host  string
		reads []string
	}{
		{"a pull request on github.com", "", []string{"POST graphql", "GET user", "GET " + afkMergeComparison}},
		{"a pull request on another host", "github.example.com", []string{"gh api graphql", "gh api user", "gh api " + afkMergeComparison}},
	} {
		t.Run(row.name, func(t *testing.T) {
			// Arrange
			runner := &afkMergeRunner{host: row.host}

			// Act
			exit, _, stderr := runner.merge("--verified", "gate run 41 passed and its test output was read")

			// Assert
			if exit != 0 || !runner.merged() {
				t.Fatalf("exit=%d stderr=%q events=%q, want it merged", exit, stderr, runner.events)
			}
			var reads []string
			for _, event := range runner.events {
				if strings.HasPrefix(event, "gh api") || strings.HasPrefix(event, "GET ") || strings.HasPrefix(event, "POST ") {
					reads = append(reads, event)
				}
			}
			if !slices.Equal(reads, row.reads) {
				t.Errorf("reads = %q, want %q", reads, row.reads)
			}
		})
	}
}
