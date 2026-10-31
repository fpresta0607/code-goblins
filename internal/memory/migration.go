package memory

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

type Move struct {
	ID            string `json:"id"`
	Label         string `json:"label"`
	Start         int    `json:"start"`
	End           int    `json:"end"`
	SHA256        string `json:"sha256"`
	Authority     string `json:"authority"`
	Destination   string `json:"destination"`
	Scope         string `json:"scope"`
	NeedsApproval bool   `json:"needs_approval"`
	ApprovalState string `json:"approval_state"`
}

type FilePlan struct {
	Path           string `json:"path"`
	Bytes          int    `json:"bytes"`
	SHA256         string `json:"sha256"`
	RetainedSHA256 string `json:"retained_sha256"`
	Moves          []Move `json:"moves"`
}

type MigrationPlan struct {
	IsDryRun      bool       `json:"is_dry_run"`
	SourceBytes   int        `json:"source_bytes"`
	RetainedBytes int        `json:"retained_bytes"`
	Files         []FilePlan `json:"files"`
	PlanSHA256    string     `json:"plan_sha256"`
	AuthorityRule string     `json:"authority_rule"`
}

// PlanMigration inventories exact byte ranges only. It has no apply operation.
// Legacy classification is conservative: every overlord.md byte is a ruling.
func PlanMigration(dataDir string) (MigrationPlan, error) {
	names, err := GovernedFiles(dataDir)
	if err != nil {
		return MigrationPlan{}, err
	}
	plan := MigrationPlan{IsDryRun: true, Files: []FilePlan{}, AuthorityRule: "Proposals only. Each pinned item needs its own explicit Overlord approval, verified privacy, and a live discoverable destination before removal. Sources remain unchanged."}
	for _, name := range names {
		content, err := ReadData(dataDir, name)
		if err != nil {
			return MigrationPlan{}, fmt.Errorf("inventory %s: %w", name, err)
		}
		file := FilePlan{Path: name, Bytes: len(content), SHA256: Hash(content), Moves: []Move{}}
		var retained bytes.Buffer
		boundaries := []int{0}
		offset := 0
		for _, line := range bytes.SplitAfter(content, []byte("\n")) {
			if offset > 0 && (bytes.HasPrefix(line, []byte("## ")) || bytes.HasPrefix(line, []byte("- "))) {
				boundaries = append(boundaries, offset)
			}
			offset += len(line)
		}
		if len(content) > 0 {
			boundaries = append(boundaries, len(content))
		}
		for index := 0; index+1 < len(boundaries); index++ {
			start, end := boundaries[index], boundaries[index+1]
			chunk := content[start:end]
			hash := Hash(chunk)
			authority, scope, approval := "fact", "fleet; confirm relevance before activating", "not-required-for-non-pinned"
			isPinned := name == "overlord.md" || bytes.Contains(chunk, []byte("<!--P-->"))
			if name == "overlord.md" {
				authority = "ruling"
			} else if name == "projects.md" {
				authority, scope = "project", "project; classify its named project before activating"
			} else if name == "memory-archive.md" {
				authority, scope = "history", "cold archive; retain provenance"
			}
			if isPinned {
				approval = "per-item-approval-required"
			}
			pathHash := Hash([]byte(name))[:12]
			id := fmt.Sprintf("%s-%s-%d", pathHash, hash[:12], start)
			label, _, _ := strings.Cut(strings.TrimSpace(string(chunk)), "\n")
			if runes := []rune(label); len(runes) > 120 {
				label = string(runes[:120]) + "... (display cue only; full bytes retained at source range)"
			}
			destination := "memory/cold/" + pathHash + "-" + hash + ".md"
			if name == "memory-archive.md" || strings.HasPrefix(name, "memory/cold/") {
				destination = name
			}
			file.Moves = append(file.Moves, Move{ID: id, Label: label, Start: start, End: end, SHA256: hash, Authority: authority, Destination: destination, Scope: scope, NeedsApproval: isPinned, ApprovalState: approval})
			retained.Write(chunk)
		}
		file.RetainedSHA256 = Hash(retained.Bytes())
		if file.SHA256 != file.RetainedSHA256 || retained.Len() != len(content) {
			return MigrationPlan{}, fmt.Errorf("inventory failed to retain all bytes of %s", name)
		}
		plan.SourceBytes += len(content)
		plan.RetainedBytes += retained.Len()
		plan.Files = append(plan.Files, file)
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		return MigrationPlan{}, err
	}
	plan.PlanSHA256 = Hash(encoded)
	return plan, nil
}
