package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// afkAuthority is AFK mode as cfo pr merge meets it: nil while it is off.
// While it is on a merge is the CFO's own merge word, so it is checked
// against what the Overlord's authority names and logged with its evidence.
type afkAuthority struct {
	// log sends one line to AFK mode's log.
	log func(afk.Entry) error
}

// afkAuthorityOf reads whether AFK mode is on in the runtime's home. With no
// home there is no fleet whose switch could be on. A switch that cannot be
// read is an error: whether a merge is the CFO's own decision is then unknown.
func afkAuthorityOf(runtime commandRuntime) (*afkAuthority, error) {
	if runtime.resolveHome == nil {
		return nil, nil
	}
	h, err := runtime.resolveHome()
	if err != nil {
		return nil, nil
	}
	switched, err := afk.Read(h.State)
	if err != nil || !switched.On {
		return nil, err
	}
	return &afkAuthority{log: func(entry afk.Entry) error { return runtime.afkLog()(h, entry) }}, nil
}

// afkLog is how a command logs a decision under AFK mode: over the
// supervisor's pipe, unless the runtime names another way.
func (r commandRuntime) afkLog() func(home.Home, afk.Entry) error {
	if r.logAFK != nil {
		return r.logAFK
	}
	return supervisor.LogAFKDecision
}

// afkMergeEvidence checks a pull request against what the Overlord's
// authority names for a merge word given without him, beyond the green,
// mergeable head verifyPRReady already proved, and returns what it read as
// the merge word's evidence.
//
// The pull request is a goblin's: opened by the account gh is signed in as,
// from a branch of the same repository. And its CI tested what lands: its
// head holds the tip of its base branch, so the merge ref's first parent is
// that tip and the merge commit's tree is the head's own, whichever base
// commit a check run started from.
func afkMergeEvidence(ctx context.Context, pullRequest, verified string, proof prProof, commands execx.Runner) (string, error) {
	repository, err := pullRequestRepository(pullRequest)
	if err != nil {
		return "", err
	}
	if proof.CrossRepository {
		return "", errors.New("this pull request comes from another repository, so it is not a goblin's branch here: leave it for the Overlord")
	}
	viewer, err := ghText(ctx, commands, "api", "user", "--jq", ".login")
	if err != nil {
		return "", fmt.Errorf("GitHub did not say which account gh is signed in as: %w", err)
	}
	if proof.Author == "" || viewer == "" {
		return "", errors.New("GitHub did not say whose pull request this is, so it cannot be told from a teammate's")
	}
	if !strings.EqualFold(proof.Author, viewer) {
		return "", fmt.Errorf("this pull request is %s's, and merging a teammate's pull request stays the Overlord's alone: leave it for him", proof.Author)
	}
	if proof.BaseRefName == "" {
		return "", errors.New("GitHub did not say which branch this pull request merges into")
	}
	compared, err := ghText(ctx, commands, "api", "repos/"+repository+"/compare/"+proof.BaseRefName+"..."+proof.HeadRefOID, "--jq", "{behind_by, base: .base_commit.sha}")
	if err != nil {
		return "", fmt.Errorf("GitHub did not compare the head with %s: %w", proof.BaseRefName, err)
	}
	var comparison struct {
		BehindBy *int   `json:"behind_by"`
		Base     string `json:"base"`
	}
	if err := json.Unmarshal([]byte(compared), &comparison); err != nil || comparison.BehindBy == nil || comparison.Base == "" {
		return "", fmt.Errorf("GitHub's comparison of the head with %s could not be read", proof.BaseRefName)
	}
	if *comparison.BehindBy > 0 {
		return "", fmt.Errorf("the head is %d commit(s) behind %s, so CI on it did not test %s as it is now: have its goblin merge %s in and let CI run on that head", *comparison.BehindBy, proof.BaseRefName, proof.BaseRefName, proof.BaseRefName)
	}
	checks := "checks"
	if proof.Checks == 1 {
		checks = "check"
	}
	return fmt.Sprintf("verified: %s; head %s; %d %s completed green; mergeable; by %s, the account gh is signed in as; %s's tip %s is in the head, so the merge ref's first parent is that tip and CI on this head tested what lands",
		verified, proof.HeadRefOID, proof.Checks, checks, proof.Author, proof.BaseRefName, comparison.Base), nil
}

// pullRequestRepository is the owner/name of the repository a pull request's
// URL names.
func pullRequestRepository(pullRequest string) (string, error) {
	parsed, err := url.Parse(pullRequest)
	if err != nil {
		return "", fmt.Errorf("%s is not a pull request's URL", pullRequest)
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) != 4 || parts[2] != "pull" || parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf("%s is not a pull request's URL, so its repository cannot be read", pullRequest)
	}
	return parts[0] + "/" + parts[1], nil
}

// ghText runs gh and returns what it printed, trimmed.
func ghText(ctx context.Context, commands execx.Runner, args ...string) (string, error) {
	res, err := commands.Run(ctx, execx.Request{Name: "gh", Args: args})
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("gh exited %d: %s", res.ExitCode, strings.TrimSpace(string(res.Stderr)))
	}
	return strings.TrimSpace(string(res.Stdout)), nil
}
