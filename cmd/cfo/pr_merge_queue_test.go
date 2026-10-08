package main

import (
	"bytes"
	"slices"
	"strings"
	"testing"
)

const queuedPRURL = "https://github.com/o/r/pull/13"

// mergeCalls are the gh pr merge calls a runner saw.
func mergeCalls(runner *prMergeRunner) [][]string {
	var calls [][]string
	for _, request := range runner.requests {
		if request.Name == "gh" && len(request.Args) > 1 && request.Args[0] == "pr" && request.Args[1] == "merge" {
			calls = append(calls, request.Args)
		}
	}
	return calls
}

// A base that requires a merge queue merges only through it: the pull
// request is added to the queue, pinned to the head that was verified, with
// no merge method of its own (the queue's rule names it) and never --admin,
// which would merge around the queue. Without a queue it merges directly, as
// before.
func TestPRMergeAddsThePullRequestToARequiredMergeQueue(t *testing.T) {
	for name, test := range map[string]struct {
		queue  bool
		want   []string
		stdout string
	}{
		"a base that requires a merge queue": {queue: true, want: []string{"pr", "merge", queuedPRURL, "--match-head-commit", prMergeHeadOID}, stdout: "added to the base's merge queue"},
		"a base with no merge queue":         {want: []string{"pr", "merge", queuedPRURL, "--merge", "--match-head-commit", prMergeHeadOID}, stdout: "Merged pull request #13"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			runner := &prMergeRunner{queue: test.queue}
			var stdout, stderr bytes.Buffer

			// Act
			exit := runPRMerge([]string{queuedPRURL}, &stdout, &stderr, runner, nil)

			// Assert
			calls := mergeCalls(runner)
			if exit != 0 || len(calls) != 1 || !slices.Equal(calls[0], test.want) {
				t.Fatalf("exit=%d gh pr merge calls %q, want exactly %q; stderr=%s", exit, calls, test.want, stderr.String())
			}
			if !strings.Contains(stdout.String(), test.stdout) {
				t.Errorf("stdout %q does not say %q", stdout.String(), test.stdout)
			}
		})
	}
}

// What a queue holds is not touched again, a queue that cannot be read is no
// reason to merge around it, and a branch is not deleted under a merge the
// queue has yet to make: in each, nothing is merged and nothing deleted.
func TestPRMergeMergesNothingAroundAMergeQueue(t *testing.T) {
	for name, test := range map[string]struct {
		runner *prMergeRunner
		args   []string
		exit   int
		says   string
	}{
		"a pull request already in the queue":   {runner: &prMergeRunner{queue: true, queued: true}, exit: 0, says: "is already in the base's merge queue"},
		"a queue GitHub does not answer for":    {runner: &prMergeRunner{queueExit: 1}, exit: 1, says: "did not say whether"},
		"deleting the branch of a queued merge": {runner: &prMergeRunner{queue: true, localOid: prMergeHeadOID}, args: []string{"--delete-branch"}, exit: 2, says: "merges through its merge queue"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			var stdout, stderr bytes.Buffer

			// Act
			exit := runPRMerge(append([]string{queuedPRURL}, test.args...), &stdout, &stderr, test.runner, nil)

			// Assert
			if exit != test.exit || !strings.Contains(stdout.String()+stderr.String(), test.says) {
				t.Fatalf("exit=%d stdout=%q stderr=%q, want exit %d saying %q", exit, stdout.String(), stderr.String(), test.exit, test.says)
			}
			if calls := mergeCalls(test.runner); len(calls) != 0 {
				t.Errorf("gh pr merge ran %q, want no merge", calls)
			}
			if test.runner.ran("gh", "api", "--method", "DELETE") || test.runner.ran("git", "branch", "-D") {
				t.Error("a branch was deleted")
			}
		})
	}
}

// While AFK mode is on, a base that requires a merge queue tests the merge
// itself, so the CFO's merge word does not need the head to hold the base's
// tip: the word's evidence says the queue decides, and its outcome is that
// the pull request joined the queue, not that it merged.
func TestAnAFKMergeIntoAQueueLeavesTheTestOfTheMergeToTheQueue(t *testing.T) {
	// Arrange: the head is behind its base, which a merge without a queue
	// refuses.
	runner := &afkMergeRunner{queue: true, behind: 2}

	// Act
	exit, stdout, stderr := runner.merge("--verified", "gate run 41 passed and its test output was read")

	// Assert
	if exit != 0 || !strings.Contains(stdout, "added to main's merge queue") {
		t.Fatalf("exit=%d stdout=%q stderr=%q, want it added to the merge queue", exit, stdout, stderr)
	}
	if len(runner.logged) != 2 {
		t.Fatalf("logged %+v, want the merge word and its outcome", runner.logged)
	}
	if evidence := runner.logged[0].Evidence; !strings.Contains(evidence, "main requires its merge queue, which tests this head merged on main's tip") {
		t.Errorf("the merge word's evidence %q does not say the queue tests the merge", evidence)
	}
	if outcome := runner.logged[1].Outcome; outcome != "added to the merge queue" {
		t.Errorf("the outcome is %q, want that it joined the merge queue", outcome)
	}
	if slices.ContainsFunc(runner.events, func(event string) bool { return strings.HasPrefix(event, "gh api repos/") }) {
		t.Errorf("events = %q, want no comparison of the head with its base", runner.events)
	}
}
