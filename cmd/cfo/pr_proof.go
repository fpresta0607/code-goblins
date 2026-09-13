package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

type prProof struct {
	HeadRefOID string
	Checks     int
}

type prProofCheckView struct {
	TypeName   string `json:"__typename"`
	Name       string `json:"name"`
	Context    string `json:"context"`
	Status     string `json:"status"`
	State      string `json:"state"`
	Conclusion string `json:"conclusion"`
}

type prProofCheck struct {
	Name       string
	Status     string
	Conclusion string
}

type prProofView struct {
	State          string             `json:"state"`
	IsDraft        bool               `json:"isDraft"`
	Mergeable      string             `json:"mergeable"`
	ReviewDecision string             `json:"reviewDecision"`
	HeadRefOID     string             `json:"headRefOid"`
	Checks         []prProofCheckView `json:"statusCheckRollup"`
}

// verifyPRReady turns a human convention ("make sure CI is green") into a
// fail-closed delivery invariant. It returns the exact verified head commit so
// the merge can be pinned to the evidence that was inspected.
func verifyPRReady(ctx context.Context, url string, commands execx.Runner) (prProof, error) {
	res, err := commands.Run(ctx, execx.Request{Name: "gh", Args: []string{
		"pr", "view", url,
		"--json", "state,isDraft,mergeable,reviewDecision,headRefOid,statusCheckRollup",
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
		case "SUCCESS", "NEUTRAL", "SKIPPED":
			// GitHub treats these as terminal non-failing conclusions.
		default:
			return prProof{}, fmt.Errorf("production gate: %s concluded %s", name, emptyAs(conclusion, "unknown"))
		}
	}

	return prProof{HeadRefOID: view.HeadRefOID, Checks: len(view.Checks)}, nil
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
