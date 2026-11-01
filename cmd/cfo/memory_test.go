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

	"github.com/fpresta0607/code-goblins/internal/memory"
)

func memoryReadOnlyCommands(t *testing.T) []struct {
	name string
	args []string
	exit int
} {
	t.Helper()
	t.Setenv("CFO_HOME", "")
	t.Setenv("CFO_STATE_OVERRIDE", "")
	dir := t.TempDir()
	writeMemoryCatalog(t, dir, "Current ruling.\n", "Archived incident.\n")
	return []struct {
		name string
		args []string
		exit int
	}{
		{"warm", []string{"memory", "retrieve", "--data", dir}, 0},
		{"exact", []string{"memory", "retrieve", "--data", dir, "--id", "incident"}, 0},
		{"project", []string{"memory", "retrieve", "--data", dir, "--project", "northwind"}, 0},
		{"task", []string{"memory", "retrieve", "--data", dir, "--task", "task-7"}, 0},
		{"topic", []string{"memory", "retrieve", "--data", dir, "--topic", "incident"}, 0},
		{"routes", []string{"memory", "routes", "--data", dir}, 0},
		{"budget", []string{"memory", "budget", "--data", dir}, 0},
		{"migration-plan", []string{"memory", "migration-plan", "--data", dir}, 0},
		{"missing item", []string{"memory", "retrieve", "--data", dir, "--id", "absent"}, 1},
		{"invalid operation", []string{"memory", "unknown", "--data", dir}, 2},
	}
}

func writeMemoryCatalog(t *testing.T, dir, active, history string) {
	t.Helper()
	content := []byte(active + history)
	if err := os.MkdirAll(filepath.Join(dir, "memory"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "overlord.md"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	catalog := memory.Catalog{Version: 1, Sources: []memory.Source{{Path: "overlord.md", SHA256: memory.Hash(content), Entries: []memory.Entry{
		{ID: "rule", End: len(active), SHA256: memory.Hash([]byte(active)), Authority: "ruling", Location: "warm"},
		{ID: "incident", Start: len(active), End: len(content), SHA256: memory.Hash([]byte(history)), Authority: "history", Location: "cold", Trigger: "incident recovery", Projects: []string{"northwind"}, Tasks: []string{"task-7"}, Topics: []string{"incident"}, Approval: &memory.Approval{By: "Overlord", Quote: "Offload incident only.", Date: "2026-10-30", Destination: "overlord.md#incident"}},
	}}}}
	encoded, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "memory", "startup.json"), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryCommandsAreReadOnlyInAnotherAgentsSession(t *testing.T) {
	for _, command := range memoryReadOnlyCommands(t) {
		t.Run(command.name, func(t *testing.T) {
			inAnotherAgentsSession(t)
			var output, diagnostics bytes.Buffer

			exit := runWithRuntime(command.args, &output, &diagnostics, testCommandRuntime(t))

			if exit != command.exit || strings.Contains(diagnostics.String(), "not the CFO's own session") {
				t.Fatalf("read-only command exit=%d, want %d: %s", exit, command.exit, diagnostics.String())
			}
			if exit == 0 && output.Len() == 0 {
				t.Fatal("memory consumer produced no output")
			}
			if command.name == "warm" && (!strings.Contains(output.String(), "Current ruling.") || strings.Contains(output.String(), "Archived incident.")) {
				t.Fatalf("startup included unrelated history: %s", output.String())
			}
			if command.name == "exact" && (!strings.Contains(output.String(), "Archived incident.") || strings.Contains(output.String(), "Current ruling.")) {
				t.Fatalf("exact retrieval included unrelated authority: %s", output.String())
			}
			if command.name == "project" || command.name == "task" || command.name == "topic" {
				if !strings.Contains(output.String(), "Archived incident.") {
					t.Fatalf("scoped route was not retrievable: %s", output.String())
				}
			}
		})
	}
}

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

func TestMemoryPackageBudgetUsesConfiguredAllowanceForEquivalentDirectories(t *testing.T) {
	t.Setenv("CFO_HOME", "")
	t.Setenv("CFO_STATE_OVERRIDE", "")
	root := t.TempDir()
	dir := filepath.Join(root, "data")
	for _, directory := range []string{dir, filepath.Join(root, "config")} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "config", "startup-context-budget"), []byte("7"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "input"), []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	var packages []memory.Package
	for _, harness := range []string{"claude", "codex", "pi"} {
		startup := memory.Package{Harness: harness}
		for _, kind := range []string{"core", "global", "memory", "skills-index", "brief", "digest"} {
			startup.Inputs = append(startup.Inputs, memory.Input{Kind: kind, Path: "input"})
		}
		packages = append(packages, startup)
	}
	encoded, err := json.Marshal(packages)
	if err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(root, "packages.json")
	if err := os.WriteFile(manifest, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(dir, "child")
	if err := os.Mkdir(child, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ workingDir, dataDir string }{
		{root, dir}, {root, dir + "/"}, {root, dir + string(filepath.Separator)},
		{root, "data"}, {root, "data/"}, {root, "data" + string(filepath.Separator)},
		{dir, "."}, {dir, "./"}, {dir, "." + string(filepath.Separator)}, {dir, "../data"},
		{child, ".."}, {child, "../"}, {child, ".." + string(filepath.Separator)}, {child, "../../data/"},
	} {
		t.Run(test.workingDir+"/"+test.dataDir, func(t *testing.T) {
			t.Chdir(test.workingDir)
			var output, diagnostics bytes.Buffer

			code := run([]string{"memory", "budget", "--data", test.dataDir, "--packages", manifest}, &output, &diagnostics)

			var reports []memory.PackageReport
			if err := json.Unmarshal(output.Bytes(), &reports); err != nil || code != 0 || len(reports) != 3 {
				t.Fatalf("package budget exit=%d, output=%s, diagnostics=%s, error=%v", code, output.String(), diagnostics.String(), err)
			}
			for _, report := range reports {
				if report.Budget != 7 || report.Bytes != 30 || report.EstimatedTokens != 8 || !report.IsOverBudget {
					t.Fatalf("equivalent directory bypassed the package allowance: %+v", report)
				}
			}
		})
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
