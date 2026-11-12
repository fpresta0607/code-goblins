package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/memory"
)

func TestMemorySearchAndDeliveredReceiptThroughCLI(t *testing.T) {
	dir := t.TempDir()
	writeMemoryCatalog(t, dir, "Current ruling.\n", "Archived incident recovery.\n")
	var first, diagnostics bytes.Buffer
	exit := runMemory([]string{"retrieve", "--data", dir, "--search", "recovery", "--limit", "1"}, &first, &diagnostics)
	if exit != 0 || !strings.Contains(first.String(), "Archived incident recovery.") || !strings.Contains(first.String(), "Current ruling.") {
		t.Fatalf("search: %d %s %s", exit, first.String(), diagnostics.String())
	}
	var keys []string
	for _, word := range strings.Fields(first.String()) {
		if strings.HasPrefix(word, "delivery=") {
			keys = append(keys, strings.TrimPrefix(word, "delivery="))
		}
	}
	encoded, err := json.Marshal(keys)
	if err != nil {
		t.Fatal(err)
	}
	receipt := filepath.Join(dir, "delivered.json")
	if err := os.WriteFile(receipt, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	var repeated bytes.Buffer
	exit = runMemory([]string{"retrieve", "--data", dir, "--search", "recovery", "--seen", receipt}, &repeated, &diagnostics)
	if exit != 0 || strings.Contains(repeated.String(), "Current ruling.") || strings.Contains(repeated.String(), "Archived incident recovery.") {
		t.Fatalf("repeated delivery: %d %s %s", exit, repeated.String(), diagnostics.String())
	}
	for _, args := range [][]string{
		{"retrieve", "--search", "recovery", "--limit", "0"},
		{"retrieve", "--id", "rule", "--search", "recovery"},
		{"retrieve", "--seen", "missing.json"},
		{"retention-plan"},
	} {
		var output, errors bytes.Buffer
		args = append(args, "--data", dir)
		if code := runMemory(args, &output, &errors); code == 0 || output.Len() != 0 {
			t.Fatalf("invalid arguments succeeded: %v %d %s", args, code, output.String())
		}
	}
}

func TestMemoryRetentionPlanThroughCLIIsReadOnly(t *testing.T) {
	inAnotherAgentsSession(t)
	dir := t.TempDir()
	for _, name := range []string{"summary.md", "recovery.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("completed fact"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	proposal := memory.RetentionProposal{Path: "summary.md", Destination: "recovery.md", SHA256: memory.Hash([]byte("completed fact")), Kind: "summary", TaskState: "completed", IsLanded: true, IsCaptureComplete: true}
	encoded, err := json.Marshal([]memory.RetentionProposal{proposal})
	if err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(dir, "proposal.json")
	if err := os.WriteFile(manifest, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	var output, diagnostics bytes.Buffer
	exit := runWithRuntime([]string{"memory", "retention-plan", "--data", dir, "--manifest", manifest}, &output, &diagnostics, testCommandRuntime(t))
	var plan memory.RetentionPlan
	if err := json.Unmarshal(output.Bytes(), &plan); err != nil || exit != 0 || len(plan.Items) != 1 || !plan.Items[0].IsRecoverable || plan.ReclaimedBytes != 0 {
		t.Fatalf("plan: %s %s %v", output.String(), diagnostics.String(), err)
	}
	for _, name := range []string{"summary.md", "recovery.md"} {
		content, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(content) != "completed fact" {
			t.Fatalf("changed %s", name)
		}
	}
}
