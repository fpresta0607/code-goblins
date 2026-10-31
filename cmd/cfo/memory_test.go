package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMemoryMigrationPlanRetainsEveryByteWithoutWriting(t *testing.T) {
	// Arrange: Unicode, CRLF, preamble and a ruling with its original wording.
	dataDir := t.TempDir()
	content := []byte("# Rulings\r\n\r\n## 2026-10-30\r\nKeep my exact words: caf\u00e9.\r\n\r\n## 2026-10-31\r\nKeep the earlier ruling too.\r\n")
	source := filepath.Join(dataDir, "overlord.md")
	if err := os.WriteFile(source, content, 0o600); err != nil {
		t.Fatal(err)
	}

	// Act: repeated dry runs must agree without a migration command or timestamp.
	var first, second, errors bytes.Buffer
	for _, output := range []*bytes.Buffer{&first, &second} {
		if code := run([]string{"memory", "migration-plan", "--data", dataDir}, output, &errors); code != 0 {
			t.Fatalf("migration-plan exit %d: %s", code, errors.String())
		}
	}

	// Assert against independently counted source bytes and SHA256, never itself.
	var plan struct {
		IsDryRun      bool `json:"is_dry_run"`
		SourceBytes   int  `json:"source_bytes"`
		RetainedBytes int  `json:"retained_bytes"`
		Files         []struct {
			SHA256         string `json:"sha256"`
			RetainedSHA256 string `json:"retained_sha256"`
			Moves          []struct {
				Start         int    `json:"start"`
				End           int    `json:"end"`
				SHA256        string `json:"sha256"`
				Authority     string `json:"authority"`
				Destination   string `json:"destination"`
				NeedsApproval bool   `json:"needs_approval"`
			} `json:"moves"`
		} `json:"files"`
	}
	if err := json.Unmarshal(first.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) || !plan.IsDryRun || plan.SourceBytes != len(content) || plan.RetainedBytes != len(content) || len(plan.Files) != 1 {
		t.Fatalf("incomplete or nondeterministic dry run: %s", first.String())
	}
	file := plan.Files[0]
	expectedHash := fmt.Sprintf("%x", sha256.Sum256(content))
	if file.SHA256 != expectedHash || file.RetainedSHA256 != expectedHash || len(file.Moves) != 3 {
		t.Fatalf("lossless reconstruction failed: %s", first.String())
	}
	for _, move := range file.Moves {
		if move.SHA256 != fmt.Sprintf("%x", sha256.Sum256(content[move.Start:move.End])) || move.Authority != "ruling" || move.Destination == "" || !move.NeedsApproval {
			t.Fatalf("move lost its bytes, scope or pinned boundary: %+v", move)
		}
	}
	preserved, err := os.ReadFile(source)
	if err != nil || !bytes.Equal(content, preserved) {
		t.Fatal("dry run modified source history")
	}
	entries, err := os.ReadDir(dataDir)
	if err != nil || len(entries) != 1 {
		t.Fatal("dry run created migration state")
	}
	var output bytes.Buffer
	if code := run([]string{"memory", "migration-plan", "--data", dataDir, "--apply"}, &output, &errors); code == 0 {
		t.Fatal("memory inventory exposed a live migration operation")
	}
}

func TestMemoryBudgetReportsLegacyOverrunWithoutDroppingAuthority(t *testing.T) {
	dataDir := t.TempDir()
	content := strings.Repeat("A pinned ruling.\n", 3000)
	if err := os.WriteFile(filepath.Join(dataDir, "overlord.md"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	var output, errors bytes.Buffer
	code := run([]string{"memory", "budget", "--data", dataDir}, &output, &errors)
	var report struct {
		IsLegacy     bool `json:"is_legacy"`
		Bytes        int  `json:"bytes"`
		IsOverBudget bool `json:"is_over_budget"`
	}
	if err := json.Unmarshal(output.Bytes(), &report); err != nil || code != 0 || !report.IsLegacy || report.Bytes != len(content) || !report.IsOverBudget {
		t.Fatalf("legacy authority was silently omitted: code=%d output=%s error=%s decode=%v", code, output.String(), errors.String(), err)
	}
}

func TestMemoryHelpLoadsOnlyTheEmbeddedMemoryContract(t *testing.T) {
	for _, topic := range [][]string{{"help", "memory"}, {"help", "contract", "memory"}} {
		var output, errors bytes.Buffer
		if code := run(topic, &output, &errors); code != 0 || output.Len() == 0 {
			t.Fatalf("embedded memory help: exit=%d, error=%s", code, errors.String())
		}
	}
}
