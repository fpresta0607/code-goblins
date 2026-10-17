package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/train"
)

type prProof struct {
	HeadRefOID string
	Checks     int
	// Author is the login that opened the pull request, BaseRefName the branch
	// it merges into, and CrossRepository whether its head is in another
	// repository; a merge word given while AFK mode is on reads them.
	Author          string
	BaseRefName     string
	CrossRepository bool
}

type prProofCheckView struct {
	TypeName   string `json:"__typename"`
	Name       string `json:"name"`
	Context    string `json:"context"`
	Status     string `json:"status"`
	State      string `json:"state"`
	Conclusion string `json:"conclusion"`
}

// prProofChecks is a head's check rollup less the workflows' draft job's
// check, which reads whether the pull request is a draft and tests nothing
// (train.DraftStateCheck): it neither proves a head nor refuses one.
type prProofChecks []prProofCheckView

func (c *prProofChecks) UnmarshalJSON(data []byte) error {
	var rollup []prProofCheckView
	if err := json.Unmarshal(data, &rollup); err != nil {
		return err
	}
	*c = slices.DeleteFunc(rollup, func(check prProofCheckView) bool {
		return check.TypeName == "CheckRun" && check.Name == train.DraftStateCheck
	})
	return nil
}

type prProofCheck struct {
	Name       string
	Status     string
	Conclusion string
}

type prProofView struct {
	State          string        `json:"state"`
	IsDraft        bool          `json:"isDraft"`
	Mergeable      string        `json:"mergeable"`
	ReviewDecision string        `json:"reviewDecision"`
	HeadRefOID     string        `json:"headRefOid"`
	Checks         prProofChecks `json:"statusCheckRollup"`
	Author         struct {
		Login string `json:"login"`
	} `json:"author"`
	BaseRefName       string `json:"baseRefName"`
	IsCrossRepository bool   `json:"isCrossRepository"`
}

// verifyPRReady turns a human convention ("make sure CI is green") into a
// fail-closed delivery invariant. It returns the exact verified head commit so
// the merge can be pinned to the evidence that was inspected.
func verifyPRReady(ctx context.Context, url string, commands execx.Runner) (prProof, error) {
	res, err := commands.Run(ctx, execx.Request{Name: "gh", Args: []string{
		"pr", "view", url,
		"--json", "state,isDraft,mergeable,reviewDecision,headRefOid,statusCheckRollup,author,baseRefName,isCrossRepository",
	}})
	if err != nil {
		return prProof{}, fmt.Errorf("production gate: inspect PR: %w", err)
	}
	if res.ExitCode != 0 {
		return prProof{}, fmt.Errorf("production gate: gh pr view exited %d: %s", res.ExitCode, strings.TrimSpace(string(res.Stderr)))
	}

	var view prProofView
	if err := json.Unmarshal(res.Stdout, &view); err != nil {
		return prProof{}, fmt.Errorf("production gate: decode PR evidence: %w", err)
	}
	if view.State != "OPEN" {
		return prProof{}, fmt.Errorf("production gate: PR state is %s, want OPEN", emptyAs(view.State, "unknown"))
	}
	if view.IsDraft {
		return prProof{}, errors.New("production gate: PR is still a draft")
	}
	if view.Mergeable == "CONFLICTING" {
		return prProof{}, errors.New("production gate: PR has merge conflicts")
	}
	if view.Mergeable == "UNKNOWN" || view.Mergeable == "" {
		return prProof{}, errors.New("production gate: mergeability is not yet known")
	}
	if view.ReviewDecision == "CHANGES_REQUESTED" {
		return prProof{}, errors.New("production gate: review changes are still requested")
	}
	if view.ReviewDecision == "REVIEW_REQUIRED" {
		return prProof{}, errors.New("production gate: required review is still pending")
	}
	if strings.TrimSpace(view.HeadRefOID) == "" {
		return prProof{}, errors.New("production gate: PR head commit is missing")
	}
	if len(view.Checks) == 0 {
		return prProof{}, errors.New("production gate: PR exposes no status checks; refusing an unverified merge")
	}

	hasRun := false
	for _, check := range view.Checks {
		normalized, err := normalizePRCheck(check)
		if err != nil {
			return prProof{}, err
		}
		name := normalized.Name
		if name == "" {
			name = "unnamed check"
		}
		status := strings.ToUpper(normalized.Status)
		conclusion := strings.ToUpper(normalized.Conclusion)
		if status != "COMPLETED" {
			return prProof{}, fmt.Errorf("production gate: %s is %s, not completed", name, emptyAs(status, "pending"))
		}
		switch conclusion {
		case "SUCCESS":
			hasRun = true
		case "NEUTRAL", "SKIPPED":
			// GitHub treats these as terminal non-failing conclusions.
		default:
			return prProof{}, fmt.Errorf("production gate: %s concluded %s", name, emptyAs(conclusion, "unknown"))
		}
	}
	// A draft's run skips its jobs, so a pull request just marked ready
	// shows them until its ready run registers: skipped and neutral checks
	// alone tested nothing.
	if !hasRun {
		return prProof{}, errors.New("production gate: no check ran on the PR's head, every one was skipped or neutral; refusing an unverified merge")
	}

	return prProof{HeadRefOID: view.HeadRefOID, Checks: len(view.Checks), Author: view.Author.Login, BaseRefName: view.BaseRefName, CrossRepository: view.IsCrossRepository}, nil
}

func normalizePRCheck(check prProofCheckView) (prProofCheck, error) {
	switch check.TypeName {
	case "CheckRun":
		return prProofCheck{Name: check.Name, Status: check.Status, Conclusion: check.Conclusion}, nil
	case "StatusContext":
		state := strings.ToUpper(check.State)
		normalized := prProofCheck{Name: emptyAs(check.Context, "unnamed status context")}
		switch state {
		case "SUCCESS", "FAILURE", "ERROR":
			normalized.Status = "COMPLETED"
			normalized.Conclusion = state
		case "PENDING", "EXPECTED":
			normalized.Status = state
		default:
			normalized.Status = emptyAs(state, "UNKNOWN")
		}
		return normalized, nil
	case "":
		return prProofCheck{}, fmt.Errorf("production gate: status check rollup entry is missing __typename (%s)", rollupEvidence(check))
	default:
		return prProofCheck{}, fmt.Errorf("production gate: unsupported status check type %q (%s)", check.TypeName, rollupEvidence(check))
	}
}

func rollupEvidence(check prProofCheckView) string {
	return fmt.Sprintf("name=%q context=%q status=%q state=%q conclusion=%q", check.Name, check.Context, check.Status, check.State, check.Conclusion)
}

func emptyAs(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
