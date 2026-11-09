package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	codegoblins "github.com/fpresta0607/code-goblins"
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
	// Oracle: the embedded command and contract references are the owned help text.
	for _, test := range []struct {
		args      []string
		reference string
	}{
		{[]string{"help", "memory"}, "docs/commands/memory.md"},
		{[]string{"help", "contract", "memory"}, "docs/contract/memory.md"},
	} {
		t.Run(test.reference, func(t *testing.T) {
			// Arrange
			want, err := fs.ReadFile(codegoblins.Contract, test.reference)
			if err != nil {
				t.Fatal(err)
			}
			var output, errors bytes.Buffer

			// Act
			code := run(test.args, &output, &errors)

			// Assert: the exact reference, so another topic or the index fails.
			if code != 0 || !bytes.Equal(output.Bytes(), want) {
				t.Fatalf("help %v printed something other than %s: exit=%d, error=%s, output=%s", test.args, test.reference, code, errors.String(), output.String())
			}
		})
	}
}

// memoryHome is an isolated CFO home whose data directory the memory
// commands and cfo session-start both read.
func memoryHome(t *testing.T) (root, dataDir string) {
	t.Helper()
	root = t.TempDir()
	t.Setenv("CFO_HOME", root)
	t.Setenv("CFO_STATE_OVERRIDE", "")
	for _, directory := range []string{filepath.Join(root, "state"), filepath.Join(root, "data", "memory"), filepath.Join(root, "config")} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return root, filepath.Join(root, "data")
}

func runCommand(args ...string) (int, string, string) {
	var output, diagnostics bytes.Buffer
	code := run(args, &output, &diagnostics)
	return code, output.String(), diagnostics.String()
}

func TestTierMarkerExamplesInCodeStayLiteralAcrossMemoryConsumers(t *testing.T) {
	// Oracle: docs/contract/memory.md and the stow skill: only an entry's
	// genuine trailing annotation carries tier metadata. CommonMark 0.31.2
	// makes fenced and indented code, including in quotes and lists, literal.
	recent := time.Now().AddDate(0, 0, -1).Format(time.DateOnly)
	approval := &memory.Approval{By: "Overlord", Quote: "Offload feedback-rule only.", Date: "2026-10-30", Destination: "memory/feedback.md#feedback-rule"}
	for _, test := range []struct {
		name, text, authority, location, expiry string
		approval                                *memory.Approval
		isIndexPinned                           bool
		failure, tier, stow, legacyTier         string
		isLegacyApproval                        bool
	}{
		{name: "indented aging example ends an unapproved cold ruling", text: "Overlord ruling: mark aging facts like this:\n\n    <!--a:2026-10-30-->\n", authority: "ruling", location: "cold", failure: "pinned offload requires", legacyTier: "aging"},
		{name: "indented aging example ends an approved cold ruling", text: "Overlord ruling: mark aging facts like this:\n\n    <!--a:2026-10-30-->\n", authority: "ruling", location: "cold", approval: approval, tier: "pinned", legacyTier: "aging"},
		{name: "indented perishable example", text: "Mark perishable facts like this:\n\n    <!--p:2026-10-30-->\n", authority: "fact", location: "warm", tier: "aging", stow: "legacy=true", legacyTier: "aging"},
		{name: "example in an index-pinned file", text: "Mark aging facts like this:\n\n    <!--a:2026-10-30-->\n", authority: "fact", location: "warm", isIndexPinned: true, tier: "pinned", legacyTier: "pinned", isLegacyApproval: true},
		{name: "pending-capture example", text: "Capture markers look like this:\n\n    <!--t:team-->\n", authority: "ruling", location: "warm", tier: "pinned", legacyTier: "aging"},
		{name: "blockquoted fence example", text: "Overlord ruling quoting its example:\n\n> ```\n> <!--a:2026-10-30-->\n", authority: "ruling", location: "warm", tier: "pinned", legacyTier: "aging"},
		{name: "list item example", text: "- Mark perishable facts like this:\n\n      <!--p:2026-10-30-->\n", authority: "fact", location: "warm", tier: "aging", stow: "legacy=true", legacyTier: "aging"},
		{name: "genuine aging marker after an example", text: "Use the local cache; a pin looks like this:\n\n    <!--P-->\n\n<!--a:" + recent + "-->\n", authority: "fact", location: "warm", tier: "aging", legacyTier: "aging"},
		{name: "genuine perishable marker", text: "Port 4300 is free until the release ships. <!--p:" + recent + "-->\n", authority: "fact", location: "warm", expiry: "until the release ships", tier: "perishable", legacyTier: "perishable"},
		{name: "genuine pin marker", text: "Keep this owner preference. <!--P-->\n", authority: "fact", location: "warm", tier: "pinned", legacyTier: "pinned", isLegacyApproval: true},
		{name: "genuine grace marker", text: "An unconfirmed legacy fact. <!--g-->\n", authority: "fact", location: "warm", tier: "aging", stow: "grace=true", legacyTier: "aging"},
		{name: "genuine pending-capture marker", text: "Report the port change to the team. <!--t:team-->\n", authority: "fact", location: "warm", tier: "aging", stow: "pending-capture=team", legacyTier: "aging"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			_, dataDir := memoryHome(t)
			content := []byte(test.text)
			if err := os.WriteFile(filepath.Join(dataDir, "memory", "feedback.md"), content, 0o600); err != nil {
				t.Fatal(err)
			}
			entry := memory.Entry{ID: "feedback-rule", End: len(content), SHA256: memory.Hash(content), Authority: test.authority, Location: test.location, Expiry: test.expiry, Approval: test.approval}
			if test.location == "cold" {
				entry.Trigger, entry.Topics = "feedback", []string{"feedback"}
			}
			catalog := memory.Catalog{Version: 1, Sources: []memory.Source{{Path: "memory/feedback.md", SHA256: memory.Hash(content), Entries: []memory.Entry{entry}}}}
			if test.isIndexPinned {
				index := []byte("[Preference](feedback.md) - owner choice <!--P-->\n")
				if err := os.WriteFile(filepath.Join(dataDir, "memory", "MEMORY.md"), index, 0o600); err != nil {
					t.Fatal(err)
				}
				catalog.Sources = append(catalog.Sources, memory.Source{Path: "memory/MEMORY.md", SHA256: memory.Hash(index), Entries: []memory.Entry{{ID: "index", End: len(index), SHA256: memory.Hash(index), Authority: "fact", Location: "warm"}}})
			}
			encoded, err := json.Marshal(catalog)
			if err != nil {
				t.Fatal(err)
			}
			catalogPath := filepath.Join(dataDir, "memory", "startup.json")
			if err := os.WriteFile(catalogPath, encoded, 0o600); err != nil {
				t.Fatal(err)
			}

			// Act
			retrieveCode, retrieved, retrieveErrors := runCommand("memory", "retrieve", "--data", dataDir, "--id", "feedback-rule")
			budgetCode, _, budgetErrors := runCommand("memory", "budget", "--data", dataDir)
			planCode, planned, planErrors := runCommand("memory", "migration-plan", "--data", dataDir)
			startupCode, startup, _ := runCommand("session-start")
			if err := os.Remove(catalogPath); err != nil {
				t.Fatal(err)
			}
			legacyCode, legacyPlanned, legacyErrors := runCommand("memory", "migration-plan", "--data", dataDir)

			// Assert
			move := func(output string) memory.Move {
				t.Helper()
				var plan memory.MigrationPlan
				if err := json.Unmarshal([]byte(output), &plan); err != nil {
					t.Fatalf("inventory is not JSON: %v: %s", err, output)
				}
				for _, file := range plan.Files {
					if file.Path == "memory/feedback.md" && len(file.Moves) == 1 {
						return file.Moves[0]
					}
				}
				t.Fatalf("inventory lost the source range: %s", output)
				return memory.Move{}
			}
			if test.failure != "" {
				for _, consumer := range []struct {
					name        string
					code        int
					diagnostics string
				}{{"retrieve", retrieveCode, retrieveErrors}, {"budget", budgetCode, budgetErrors}, {"migration-plan", planCode, planErrors}} {
					if consumer.code != 1 || !strings.Contains(consumer.diagnostics, test.failure) {
						t.Errorf("%s accepted a literal example as authority metadata: exit=%d, %s", consumer.name, consumer.code, consumer.diagnostics)
					}
				}
				if startupCode != 0 || !strings.Contains(startup, "MEMORY UNREADABLE") || !strings.Contains(startup, test.failure) {
					t.Errorf("startup hid the authority diagnostic: %s", startup)
				}
			} else {
				if retrieveCode != 0 || !strings.Contains(retrieved, " tier="+test.tier+" delivery=") || !strings.Contains(retrieved, test.text) {
					t.Errorf("retrieve classified the entry wrongly: exit=%d, %s%s", retrieveCode, retrieved, retrieveErrors)
				}
				if test.stow == "" && strings.Contains(retrieved, "STOW REQUIRED") || test.stow != "" && !strings.Contains(retrieved, test.stow) {
					t.Errorf("retrieve reported the wrong Stow diagnostic, want %q: %s", test.stow, retrieved)
				}
				if budgetCode != 0 {
					t.Errorf("budget refused valid memory: %s", budgetErrors)
				}
				if planCode != 0 || move(planned).Tier != test.tier {
					t.Errorf("catalog inventory classified the entry wrongly: exit=%d, %s%s", planCode, planned, planErrors)
				}
				if startupCode != 0 || strings.Contains(startup, "MEMORY UNREADABLE") || test.location == "warm" && !strings.Contains(startup, test.text) {
					t.Errorf("startup did not deliver the valid warm entry: %s", startup)
				}
			}
			if legacyCode != 0 {
				t.Fatalf("legacy inventory failed: %s", legacyErrors)
			}
			if legacy := move(legacyPlanned); legacy.Tier != test.legacyTier || legacy.ClassificationError != "" || legacy.NeedsApproval != test.isLegacyApproval {
				t.Errorf("legacy inventory classified the range wrongly: %+v", legacy)
			}
		})
	}
}

func TestIncompleteCatalogNamesEachUncatalogedSource(t *testing.T) {
	// Arrange: Stow files new facts as their own files after the catalog exists.
	_, dataDir := memoryHome(t)
	writeMemoryCatalog(t, dataDir, "Current ruling.\n", "Archived incident.\n")
	for _, name := range []string{"new-fact.md", filepath.Join("cold", "archived.md")} {
		path := filepath.Join(dataDir, "memory", name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("A newly filed fact.\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	const omitted = "memory/cold/archived.md, memory/new-fact.md"

	// Act
	retrieveCode, _, retrieveErrors := runCommand("memory", "retrieve", "--data", dataDir)
	startupCode, startup, _ := runCommand("session-start")

	// Assert: each names the sorted paths and the catalog to amend, not a command that refuses it too.
	if retrieveCode != 1 || !strings.Contains(retrieveErrors, omitted) || !strings.Contains(retrieveErrors, "memory/startup.json") || strings.Contains(retrieveErrors, "migration-plan") {
		t.Fatalf("retrieve did not name the uncataloged sources: exit=%d, %s", retrieveCode, retrieveErrors)
	}
	start := strings.Index(startup, "MEMORY UNREADABLE")
	if startupCode != 0 || start < 0 {
		t.Fatalf("startup hid the incomplete catalog: %s", startup)
	}
	diagnostic, _, _ := strings.Cut(startup[start:], "\n")
	if !strings.Contains(diagnostic, omitted) || !strings.Contains(diagnostic, "memory/startup.json") || strings.Contains(diagnostic, "migration-plan") {
		t.Fatalf("startup diagnostic did not name the uncataloged sources: %s", diagnostic)
	}
}

func TestStartupBudgetLineAgreesWithMemoryBudget(t *testing.T) {
	// Oracle: the contract's ceil(bytes/4) estimate over the warm ranges and
	// routes startup actually delivered, measured from the emitted digest.
	for _, test := range []struct {
		allowance    int
		isOverBudget bool
	}{{1, true}, {1000000, false}} {
		t.Run(strconv.Itoa(test.allowance), func(t *testing.T) {
			// Arrange
			root, dataDir := memoryHome(t)
			writeMemoryCatalog(t, dataDir, "Current ruling.\n", "Archived incident.\n")
			if err := os.WriteFile(filepath.Join(root, "config", "startup-memory-budget"), []byte(strconv.Itoa(test.allowance)), 0o600); err != nil {
				t.Fatal(err)
			}

			// Act
			budgetCode, budgeted, budgetErrors := runCommand("memory", "budget", "--data", dataDir)
			startupCode, startup, _ := runCommand("session-start")

			// Assert
			var report memory.Report
			if err := json.Unmarshal([]byte(budgeted), &report); err != nil || budgetCode != 0 {
				t.Fatalf("budget exit=%d, %s%s: %v", budgetCode, budgeted, budgetErrors, err)
			}
			const header = "Verified warm operating map; quoted source ranges follow. No source file is claimed read in full.\n"
			ranges := strings.Index(startup, header)
			guidance := strings.Index(startup, "Before work, retrieve relevant cold authority")
			routes := guidance + strings.Index(startup[guidance:], "\n") + 1
			line := strings.Index(startup, "Warm memory and routes: ")
			if startupCode != 0 || ranges < 0 || guidance < ranges || line < routes {
				t.Fatalf("startup did not deliver warm memory and its budget line: %s", startup)
			}
			delivered := guidance - (ranges + len(header)) + line - routes
			want := fmt.Sprintf("Warm memory and routes: %d bytes, %d estimated tokens (bytes/4 rounded up), budget %d estimated tokens.\n", delivered, (delivered+3)/4, test.allowance)
			if !strings.Contains(startup, want) || strings.Contains(startup, "MEMORY OVER BUDGET") != test.isOverBudget {
				t.Fatalf("startup budget line disagrees with the delivered bytes, want %q: %s", want, startup)
			}
			if report.Bytes != delivered || report.EstimatedTokens != (delivered+3)/4 || report.Budget != test.allowance || report.IsOverBudget != test.isOverBudget {
				t.Fatalf("cfo memory budget disagrees with startup: %+v, delivered %d bytes", report, delivered)
			}
		})
	}
}
