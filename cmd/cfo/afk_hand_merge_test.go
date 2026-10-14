package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

const handMergedPull = "https://github.com/acme/api/pull/784"

// The 13:18Z report of 2026-10-14 listed eight pull requests that landed by
// train as not merged: each was logged by hand with a link and no outcome.
// The log now asks GitHub and records what it says.
func TestAFKLogRecordsTheOutcomeMergedForAMergedPullRequest(t *testing.T) {
	// Arrange
	runtime, _ := afkRuntime(t)
	var sent afk.Entry
	runtime.logAFK = func(_ home.Home, entry afk.Entry) error { sent = entry; return nil }
	var asked []string
	runtime.pullRequestState = func(_ context.Context, url string) (supervisor.PullRequestInfo, error) {
		asked = append(asked, url)
		return supervisor.PullRequestInfo{State: "MERGED"}, nil
	}

	// Act
	exit, stdout, stderr := afkCommand(runtime, "log", "--kind", "merge", "--what", "the button that grants the service", "--evidence", "Train CI tested 784 in one run", "--link", handMergedPull)

	// Assert
	if exit != 0 || sent.Outcome != afk.OutcomeMerged || len(asked) != 1 || asked[0] != handMergedPull {
		t.Fatalf("exit=%d stderr=%q outcome=%q asked=%q, want the outcome merged from one read of the pull request", exit, stderr, sent.Outcome, asked)
	}
	if !strings.Contains(stdout, "GitHub says it merged") {
		t.Errorf("stdout = %q, want it to say GitHub says the pull request merged", stdout)
	}
}

func TestAFKLogRecordsNoOutcomeForAPullRequestThatDidNotMergeOrCouldNotBeRead(t *testing.T) {
	for name, tc := range map[string]struct {
		info supervisor.PullRequestInfo
		err  error
		said string
	}{
		"it is still open":   {info: supervisor.PullRequestInfo{State: "OPEN"}},
		"it closed unmerged": {info: supervisor.PullRequestInfo{State: "CLOSED"}},
		"GitHub did not say": {err: errors.New("gh exited 1"), said: "GitHub did not say whether"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			runtime, _ := afkRuntime(t)
			var sent afk.Entry
			runtime.logAFK = func(_ home.Home, entry afk.Entry) error { sent = entry; return nil }
			runtime.pullRequestState = func(context.Context, string) (supervisor.PullRequestInfo, error) { return tc.info, tc.err }

			// Act
			exit, _, stderr := afkCommand(runtime, "log", "--kind", "merge", "--what", "tried to land it", "--evidence", "CI red", "--link", handMergedPull)

			// Assert
			if exit != 0 || sent.Kind != afk.KindMerge || sent.Outcome != "" || !strings.Contains(stderr, tc.said) {
				t.Errorf("exit=%d stderr=%q sent=%+v, want the line logged with no outcome", exit, stderr, sent)
			}
		})
	}
}

func TestAFKLogAsksGitHubOnlyAboutAMergeLineThatNamesAPullRequest(t *testing.T) {
	for name, args := range map[string][]string{
		"a deploy that links a pull request": {"--kind", "deploy", "--what", "acme production", "--evidence", "/health reads 200", "--link", handMergedPull},
		"a merge that links a run":           {"--kind", "merge", "--what", "landed", "--evidence", "passed", "--link", "https://github.com/acme/api/actions/runs/1"},
		"a merge that links nothing":         {"--kind", "merge", "--what", "landed by hand", "--evidence", "read it"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			runtime, _ := afkRuntime(t)
			runtime.logAFK = func(home.Home, afk.Entry) error { return nil }
			asked := 0
			runtime.pullRequestState = func(context.Context, string) (supervisor.PullRequestInfo, error) {
				asked++
				return supervisor.PullRequestInfo{State: "MERGED"}, nil
			}

			// Act
			exit, _, stderr := afkCommand(runtime, append([]string{"log"}, args...)...)

			// Assert
			if exit != 0 || asked != 0 {
				t.Errorf("exit=%d stderr=%q asked=%d, want the line logged and GitHub not asked", exit, stderr, asked)
			}
		})
	}
}
