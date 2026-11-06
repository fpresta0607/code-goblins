package memory

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

// RetentionProposal is an operator's declaration, not verified task state or
// permission for janitor to remove anything. All destinations stay local.
type RetentionProposal struct {
	Path              string `json:"path"`
	Destination       string `json:"destination"`
	SHA256            string `json:"sha256"`
	Kind              string `json:"kind"`
	TaskState         string `json:"task_state"`
	IsHeld            bool   `json:"is_held"`
	IsLanded          bool   `json:"is_landed"`
	IsCaptureComplete bool   `json:"is_capture_complete"`
}

type RetentionItem struct {
	RetentionProposal
	SourceSHA256   string `json:"source_sha256"`
	Bytes          int    `json:"bytes"`
	RecoverySHA256 string `json:"recovery_sha256,omitempty"`
	IsRecoverable  bool   `json:"is_recoverable"`
	Reason         string `json:"reason"`
}

type RetentionPlan struct {
	IsDryRun         bool            `json:"is_dry_run"`
	ReclaimedBytes   int             `json:"reclaimed_bytes"`
	IsRemoteVerified bool            `json:"is_remote_verified"`
	AuthorityRule    string          `json:"authority_rule"`
	Items            []RetentionItem `json:"items"`
}

// PlanRetention verifies existing recovery copies only. It writes nothing and
// intentionally exposes no apply operation or remote upload path.
func PlanRetention(dataDir string, proposals []RetentionProposal) (RetentionPlan, error) {
	plan := RetentionPlan{IsDryRun: true, Items: []RetentionItem{}, AuthorityRule: "Task state and capture are unverified operator proposals. Local recovery is not disk relief, GitHub offload or removal approval. Keep sources until the owning retention process verifies current task custody, durable capture, privacy and each pinned approval."}
	var sources []os.FileInfo
	governed, err := GovernedFiles(dataDir)
	if err != nil {
		return RetentionPlan{}, err
	}
	var governedFiles []os.FileInfo
	for _, path := range governed {
		info, err := os.Stat(filepath.Join(dataDir, filepath.FromSlash(path)))
		if err != nil {
			return RetentionPlan{}, err
		}
		governedFiles = append(governedFiles, info)
	}
	for _, proposal := range proposals {
		proposal.Path = filepath.ToSlash(filepath.Clean(filepath.FromSlash(proposal.Path)))
		content, err := ReadData(dataDir, proposal.Path)
		if err != nil {
			return RetentionPlan{}, fmt.Errorf("retention source %s: %w", proposal.Path, err)
		}
		sourceInfo, err := os.Stat(filepath.Join(dataDir, filepath.FromSlash(proposal.Path)))
		if err != nil {
			return RetentionPlan{}, err
		}
		if slices.ContainsFunc(sources, func(info os.FileInfo) bool { return os.SameFile(info, sourceInfo) }) {
			return RetentionPlan{}, fmt.Errorf("duplicate retention proposal %s", proposal.Path)
		}
		sources = append(sources, sourceInfo)
		if !slices.Contains([]string{"summary", "index", "artifact", "authority"}, proposal.Kind) || !slices.Contains([]string{"active", "held", "completed"}, proposal.TaskState) {
			return RetentionPlan{}, errors.New("retention proposal needs a known kind and explicit task_state")
		}
		item := RetentionItem{RetentionProposal: proposal, Bytes: len(content)}
		item.SourceSHA256 = Hash(content)
		switch {
		case slices.ContainsFunc(governedFiles, func(info os.FileInfo) bool { return os.SameFile(info, sourceInfo) }) || proposal.Kind == "authority":
			item.Reason = "retain authority; use migration-plan and individually named approval"
		case proposal.IsHeld || proposal.TaskState == "held":
			item.Reason = "retain held work"
		case proposal.TaskState != "completed":
			item.Reason = "retain task that is not completed"
		case !proposal.IsLanded:
			item.Reason = "retain unlanded work"
		case !proposal.IsCaptureComplete:
			item.Reason = "retain incomplete durable capture"
		case proposal.SHA256 != item.SourceSHA256:
			item.Reason = "retain: source hash changed"
		default:
			recovery, err := ReadData(dataDir, proposal.Destination)
			if err != nil {
				item.Reason = "retain: recovery unavailable: " + err.Error()
				break
			}
			recoveryInfo, err := os.Stat(filepath.Join(dataDir, filepath.FromSlash(proposal.Destination)))
			if err != nil {
				return RetentionPlan{}, err
			}
			item.RecoverySHA256 = Hash(recovery)
			if os.SameFile(sourceInfo, recoveryInfo) {
				item.Reason = "retain: recovery must be an independent file"
				break
			}
			if item.RecoverySHA256 != item.SourceSHA256 {
				item.Reason = "retain: source and recovery bytes differ"
				break
			}
			item.IsRecoverable = true
			item.Reason = "verified local recovery only; retain pending authoritative custody and durable-destination approval"
		}
		plan.Items = append(plan.Items, item)
	}
	return plan, nil
}
