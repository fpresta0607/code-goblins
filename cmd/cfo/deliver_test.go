package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// Every goblin reads its brief before it writes a commit, so the brief is
// where a standing authorship rule has to live: a goblin that never sees it
// signs the fleet's history with an author that does not exist.
func TestBriefScaffoldCarriesTheCommitAuthorshipRule(t *testing.T) {
	t.Chdir(t.TempDir())

	var stdout, stderr bytes.Buffer
	if exit := runBrief([]string{"t1", "--project", "projects/demo"}, &stdout, &stderr); exit != 0 {
		t.Fatalf("runBrief exit=%d stderr=%s", exit, stderr.String())
	}

	body, err := os.ReadFile(strings.TrimSpace(stdout.String()))
	if err != nil {
		t.Fatal(err)
	}
	// Collapsed to one space so the assertions pin the rule's presence rather
	// than where the template happens to wrap: the first version of this test
	// failed only because the rendered phrase straddled a line break.
	brief := strings.Join(strings.Fields(string(body)), " ")
	for _, want := range []string{
		"## Commits",
		"Never name an AI product",
		"Co-Authored-By",
		"pull request body",
	} {
		if !strings.Contains(brief, want) {
			t.Errorf("brief scaffold is missing %q:\n%s", want, brief)
		}
	}
}

// The commit gh reports as the PR's head, and the one an unrelated local
// branch of the same name in some other checkout happens to sit on.
const (
	prMergeHeadOID  = "3f1a9c0d5e8b47216a0d9c3f1b2e4a67d8c05f93"
	prMergeOtherOID = "aa11bb22cc33dd44ee55ff66aa77bb88cc99dd00"
)

// The gh pr view payload this code parses, in the shape gh 2.86 emits.
const prMergeHeadJSON = `{"state":"OPEN","isDraft":false,"mergeable":"MERGEABLE","reviewDecision":"APPROVED","headRefName":"fix/x","headRefOid":"` + prMergeHeadOID + `","headRepository":{"name":"code-goblins"},"headRepositoryOwner":{"login":"fpresta0607"},"statusCheckRollup":[{"name":"go","status":"COMPLETED","conclusion":"SUCCESS"}]}`

// gh's message for a DELETE of a ref that is not there, which is what a
// repository with "Automatically delete head branches" enabled produces.
const prMergeRefGone = "gh: Reference does not exist (HTTP 422)"

// git's actual refusal, copied from a reproduction: a branch checked out
// in a worktree cannot be deleted, and -D does not override it either.
const prMergeWorktreeRefusal = "error: cannot delete branch 'fix/x' used by worktree at 'C:/dev/code-goblins/.worktrees/gb-x'"

// prMergeRunner scripts the commands a branch-deleting merge makes and records
// every one of them, so a test can assert what was NOT run as easily as what
// was.
type prMergeRunner struct {
	requests []execx.Request

	mergeExit    int
	viewStdout   string
	viewExit     int
	deleteExit   int
	deleteStderr string
	// localOid is the commit `git rev-parse` resolves the local branch to; an
	// empty one is a branch that is not there. localStderr with a non-zero
	// localExit is the shape git produces for a branch held by a worktree.
	localOid    string
	localExit   int
	localStderr string
}

func (r *prMergeRunner) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	r.requests = append(r.requests, request)
	switch {
	case request.Name == "gh" && request.Args[0] == "pr" && request.Args[1] == "merge":
		if r.mergeExit != 0 {
			return execx.Result{ExitCode: r.mergeExit, Stderr: []byte("gh: merge refused")}, nil
		}
		return execx.Result{Stdout: []byte("Merged pull request #13")}, nil
	case request.Name == "gh" && request.Args[0] == "pr" && request.Args[1] == "view":
		body := r.viewStdout
		if body == "" {
			body = prMergeHeadJSON
		}
		return execx.Result{ExitCode: r.viewExit, Stdout: []byte(body)}, nil
	case request.Name == "gh" && request.Args[0] == "api":
		if r.deleteExit != 0 {
			reason := r.deleteStderr
			if reason == "" {
				reason = "HTTP 403: Resource not accessible"
			}
			return execx.Result{ExitCode: r.deleteExit, Stderr: []byte(reason)}, nil
		}
		return execx.Result{}, nil
	case request.Name == "git" && request.Args[0] == "rev-parse":
		if r.localOid == "" {
			return execx.Result{ExitCode: 1}, nil
		}
		return execx.Result{Stdout: []byte(r.localOid + "\n")}, nil
	case request.Name == "git" && request.Args[0] == "branch":
		return execx.Result{ExitCode: r.localExit, Stderr: []byte(r.localStderr)}, nil
	}
	return execx.Result{}, errors.New("unexpected command: " + request.Name)
}

func (r *prMergeRunner) ran(name string, args ...string) bool {
	for _, request := range r.requests {
		if request.Name != name || len(request.Args) < len(args) {
			continue
		}
		match := true
		for i, want := range args {
			if request.Args[i] != want {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// The defect: git refuses to delete a branch checked out in a worktree, and
// every goblin branch is checked out in one. gh's --delete-branch does both
// deletions as a single step, so that guaranteed local failure decided whether
// the remote ref survived, leaving the fork littered with the branches of
// merged work.
func TestPRMergeDeletesTheRemoteRefEvenWhenTheLocalBranchIsHeldByAWorktree(t *testing.T) {
	runner := &prMergeRunner{
		localOid:    prMergeHeadOID,
		localExit:   1,
		localStderr: prMergeWorktreeRefusal,
	}

	var stdout, stderr bytes.Buffer
	exit := runPRMerge([]string{"https://github.com/o/r/pull/13", "--delete-branch"}, &stdout, &stderr, runner)

	if exit != 0 {
		t.Fatalf("exit=%d, want 0 - the merge landed, so a leftover local branch is not a failure; stderr=%s", exit, stderr.String())
	}
	if !runner.ran("gh", "api", "--method", "DELETE", "repos/fpresta0607/code-goblins/git/refs/heads/fix/x") {
		t.Error("the remote ref was not deleted, which is the whole point of doing it independently")
	}
	// Premise: the local deletion really was attempted and really did fail.
	// Without this the test would pass on a build that stopped deleting local
	// branches at all, proving nothing about independence.
	if !runner.ran("git", "branch", "-D", "fix/x") {
		t.Error("no local deletion was attempted, so this does not test the local failure path")
	}
	if !strings.Contains(stderr.String(), "the local branch fix/x was left in place") {
		t.Errorf("stderr = %q, want the local failure reported as a warning", stderr.String())
	}
	if !strings.Contains(stderr.String(), "used by worktree") {
		t.Errorf("stderr = %q, want git's own reason carried through", stderr.String())
	}
	if !strings.Contains(stdout.String(), "deleted remote branch fix/x") {
		t.Errorf("stdout = %q, want the remote deletion reported", stdout.String())
	}
}

// gh's flag is what couples the two deletions, so not passing it is the fix.
// Asserting its absence is asserting the defect is gone.
func TestPRMergeNeverForwardsDeleteBranchToGH(t *testing.T) {
	runner := &prMergeRunner{localOid: prMergeHeadOID}

	var stdout, stderr bytes.Buffer
	if exit := runPRMerge([]string{"https://github.com/o/r/pull/13", "--delete-branch"}, &stdout, &stderr, runner); exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr.String())
	}
	for _, request := range runner.requests {
		if request.Name != "gh" || len(request.Args) < 2 || request.Args[1] != "merge" {
			continue
		}
		for _, arg := range request.Args {
			if arg == "--delete-branch" {
				t.Fatalf("gh pr merge was given --delete-branch (%v), which recouples the two deletions", request.Args)
			}
		}
	}
}

func TestPRMergeDeletesBothWhenNoWorktreeHoldsTheBranch(t *testing.T) {
	runner := &prMergeRunner{localOid: prMergeHeadOID}

	var stdout, stderr bytes.Buffer
	if exit := runPRMerge([]string{"https://github.com/o/r/pull/13", "--delete-branch"}, &stdout, &stderr, runner); exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr.String())
	}
	if !strings.Contains(stdout.String(), "deleted remote branch fix/x") || !strings.Contains(stdout.String(), "deleted local branch fix/x") {
		t.Errorf("stdout = %q, want both deletions reported", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want silence when nothing failed", stderr.String())
	}
}

// With "Automatically delete head branches" enabled the ref is already gone by
// the time the DELETE runs, and gh exits non-zero saying so. The end state the
// flag asks for was reached, so warning that the branch was left in place is
// both false and the kind of noise that trains the reader to ignore the
// warning that matters.
func TestPRMergeStaysSilentWhenTheRemoteRefIsAlreadyGone(t *testing.T) {
	runner := &prMergeRunner{deleteExit: 1, deleteStderr: prMergeRefGone, localOid: prMergeHeadOID}

	var stdout, stderr bytes.Buffer
	if exit := runPRMerge([]string{"https://github.com/o/r/pull/13", "--delete-branch"}, &stdout, &stderr, runner); exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want silence - the ref is gone, which is what was asked for", stderr.String())
	}
	if strings.Contains(stdout.String(), "deleted remote branch") {
		t.Errorf("stdout = %q, want no claim that cfo deleted a ref it did not", stdout.String())
	}
	if !strings.Contains(stdout.String(), "remote branch fix/x was already gone") {
		t.Errorf("stdout = %q, want the branch reported as gone", stdout.String())
	}
}

// A failed remote deletion must not be reported as a success, and must not
// stop the local cleanup either - the two are independent in both directions.
func TestPRMergeReportsAFailedRemoteDeletionWithoutClaimingIt(t *testing.T) {
	runner := &prMergeRunner{deleteExit: 1, localOid: prMergeHeadOID}

	var stdout, stderr bytes.Buffer
	if exit := runPRMerge([]string{"https://github.com/o/r/pull/13", "--delete-branch"}, &stdout, &stderr, runner); exit != 0 {
		t.Fatalf("exit=%d, want 0 - the merge still landed; stderr=%s", exit, stderr.String())
	}
	if strings.Contains(stdout.String(), "deleted remote branch") {
		t.Errorf("stdout = %q, want no claim that the remote ref was deleted", stdout.String())
	}
	if !strings.Contains(stderr.String(), "the remote branch fix/x was left in place") {
		t.Errorf("stderr = %q, want the remote failure reported", stderr.String())
	}
	if !runner.ran("git", "branch", "-D", "fix/x") {
		t.Error("the local cleanup was skipped because the remote one failed; they are independent")
	}
}

// Nothing is deleted for a merge that did not happen.
func TestPRMergeDeletesNothingWhenTheMergeFails(t *testing.T) {
	runner := &prMergeRunner{mergeExit: 1, localOid: prMergeHeadOID}

	var stdout, stderr bytes.Buffer
	if exit := runPRMerge([]string{"https://github.com/o/r/pull/13", "--delete-branch"}, &stdout, &stderr, runner); exit != 1 {
		t.Fatalf("exit=%d, want 1 for a refused merge", exit)
	}
	if runner.ran("gh", "api") || runner.ran("git", "branch") {
		t.Errorf("a branch was deleted for a merge that never landed: %v", runner.requests)
	}
}

// Without the flag the command must stay exactly what it was: one merge.
func TestPRMergeWithoutDeleteBranchRunsOnlyTheMerge(t *testing.T) {
	runner := &prMergeRunner{localOid: prMergeHeadOID}

	var stdout, stderr bytes.Buffer
	if exit := runPRMerge([]string{"https://github.com/o/r/pull/13"}, &stdout, &stderr, runner); exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr.String())
	}
	if len(runner.requests) != 2 {
		t.Errorf("ran %d commands, want proof + merge: %v", len(runner.requests), runner.requests)
	}
	if !runner.ran("gh", "pr", "merge", "https://github.com/o/r/pull/13", "--merge", "--match-head-commit", prMergeHeadOID) {
		t.Errorf("merge was not SHA-pinned: %v", runner.requests)
	}
}

// A branch name is not an identity. cfo is run from whatever checkout the
// operator's shell is in, goblin names like fix/x collide across projects, and
// -D force-deletes: a same-named local branch on a different commit is
// somebody else's unmerged, unpushed work.
func TestPRMergeLeavesALocalBranchOnADifferentCommitAlone(t *testing.T) {
	runner := &prMergeRunner{localOid: prMergeOtherOID}

	var stdout, stderr bytes.Buffer
	if exit := runPRMerge([]string{"https://github.com/o/r/pull/13", "--delete-branch"}, &stdout, &stderr, runner); exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr.String())
	}
	if runner.ran("git", "branch", "-D", "fix/x") {
		t.Error("force-deleted a local branch that is not the merged commit, destroying whatever it carried")
	}
	if strings.Contains(stdout.String(), "local branch") || stderr.Len() != 0 {
		t.Errorf("stdout = %q stderr = %q, want nothing said - this checkout's branch is not the merged one", stdout.String(), stderr.String())
	}
}

// A local branch that was never there is not a warning. cfo runs from
// checkouts that never held the goblin's branch, and a warning about a branch
// that does not exist trains the reader to ignore the one that matters.
func TestPRMergeStaysSilentWhenThereIsNoLocalBranch(t *testing.T) {
	runner := &prMergeRunner{localOid: ""}

	var stdout, stderr bytes.Buffer
	if exit := runPRMerge([]string{"https://github.com/o/r/pull/13", "--delete-branch"}, &stdout, &stderr, runner); exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr.String())
	}
	if runner.ran("git", "branch", "-D", "fix/x") {
		t.Error("tried to delete a local branch that does not exist")
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want silence - there was nothing local to clean up", stderr.String())
	}
}
