package memory

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCompleteStartupAccountingIncludesEveryDeliveredCategory(t *testing.T) {
	dir := t.TempDir()
	var packages []Package
	for _, harness := range []string{"codex", "claude", "pi"} {
		startup := Package{Harness: harness}
		for _, kind := range []string{"core", "global", "memory", "skills-index", "brief", "digest"} {
			name := harness + "-" + kind
			if err := os.WriteFile(filepath.Join(dir, name), []byte("12345"), 0o600); err != nil {
				t.Fatal(err)
			}
			startup.Inputs = append(startup.Inputs, Input{Kind: kind, Path: name})
		}
		packages = append(packages, startup)
	}
	reports, err := MeasurePackages(packages, dir, 7)
	if err != nil || len(reports) != 3 {
		t.Fatalf("reports=%+v, %v", reports, err)
	}
	for _, report := range reports {
		// Six delivered 5-byte inputs are 30 bytes and ceil(30/4)=8 tokens.
		if report.Bytes != 30 || report.EstimatedTokens != 8 || !report.IsOverBudget || len(report.Inputs) != 6 {
			t.Fatalf("complete startup was undercounted: %+v", report)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "codex-global"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := MeasurePackages(packages, dir, 100); err == nil {
		t.Fatal("empty startup input lacked an explicit absence or containment reason")
	}
	if err := os.WriteFile(filepath.Join(dir, "codex-global"), []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	packages[1].Inputs = packages[1].Inputs[:5]
	if _, err := MeasurePackages(packages, dir, 100); err == nil {
		t.Fatal("hook preview accepted without emitted digest")
	}
	if _, err := MeasurePackages(packages[:1], dir, 100); err == nil {
		t.Fatal("single harness accepted as all-harness accounting")
	}
	if err := os.Remove(filepath.Join(dir, "codex-core")); err != nil {
		t.Fatal(err)
	}
	if _, err := MeasurePackages(packages, dir, 100); err == nil {
		t.Fatal("missing injected source was silently counted as zero")
	}
}

func TestMemoryBudgetRejectsCorruptSettingsInsteadOfInferringAnAllowance(t *testing.T) {
	path := filepath.Join(t.TempDir(), "budget")
	if value, err := ReadBudget(path, 10000); err != nil || value != 10000 {
		t.Fatalf("absent default=%d, %v", value, err)
	}
	for _, content := range []string{"0", "-1", "many", "1000\n2000"} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadBudget(path, 10000); err == nil {
			t.Fatalf("invalid allowance %q was accepted", content)
		}
	}
}

func TestMemoryReportUsesConfiguredAllowanceForEquivalentDirectories(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "data")
	writeMemoryFile(t, dir, "overlord.md", []byte("12345"))
	writeMemoryFile(t, root, "config/startup-memory-budget", []byte("1"))
	t.Chdir(root)
	for _, dataDir := range []string{dir, dir + "/", dir + string(filepath.Separator), "data", "data/", "data" + string(filepath.Separator)} {
		t.Run(dataDir, func(t *testing.T) {
			report, err := MemoryReport(dataDir, referenceDate)

			if err != nil || report.Budget != 1 || report.Bytes != 5 || report.EstimatedTokens != 2 || !report.IsOverBudget {
				t.Fatalf("equivalent directory bypassed the configured allowance: %+v, %v", report, err)
			}
		})
	}
}

func TestMemoryReportCountsCatalogRoutesAndPreservesSourceHashes(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "data")
	active, history := []byte("Keep.\n"), []byte("Old.\n")
	writeMemoryFile(t, dir, "overlord.md", active)
	writeMemoryFile(t, dir, "memory/history.md", history)
	catalog := Catalog{Version: 1, Sources: []Source{
		{Path: "overlord.md", SHA256: Hash(active), Entries: []Entry{{ID: "rule", End: len(active), SHA256: Hash(active), Authority: "ruling", Location: "warm"}}},
		{Path: "memory/history.md", SHA256: Hash(history), Entries: []Entry{{ID: "old", End: len(history), SHA256: Hash(history), Authority: "history", Location: "cold", Trigger: "investigating history", Topics: []string{"history"}, Approval: &Approval{By: "Overlord", Quote: "Offload old.", Date: "2026-10-30", Destination: "memory/history.md#old"}}}},
	}}
	writeCatalog(t, dir, catalog)
	expected := "\n[rule] overlord.md bytes 0:6 sha256=83768329c26475abbbc68d9a5c279c8021e5b50caced12b379eed83c11ab19a2 authority=ruling tier=pinned\nKeep.\n\nold: investigating history (projects= tasks= topics=history)\n"
	loaded, err := Load(dir, referenceDate)
	if err != nil {
		t.Fatal(err)
	}
	text, err := loaded.Retrieve(Query{}, referenceDate)
	if err != nil || text+loaded.Routes(2000) != expected {
		t.Fatalf("emitted memory differs from the independently counted fixture: %q, %v", text+loaded.Routes(2000), err)
	}
	for _, test := range []struct {
		allowance    string
		isOverBudget bool
	}{{"50", false}, {"49", true}} {
		t.Run(test.allowance, func(t *testing.T) {
			writeMemoryFile(t, root, "config/startup-memory-budget", []byte(test.allowance))

			report, err := MemoryReport(dir, referenceDate)

			if err != nil || report.IsLegacy || report.Bytes != 199 || report.EstimatedTokens != 50 || report.IsOverBudget != test.isOverBudget || len(report.Inputs) != 1 {
				t.Fatalf("warm accounting disagrees with 199-byte fixture: %+v, %v", report, err)
			}
			input := report.Inputs[0]
			if input.Bytes != 199 || input.EstimatedTokens != 50 || input.SHA256 != "b81af7343c3684e5065fa05cb8cfb9375fc5e8a30f4bdccd06ab4a5e0c7f4d52" {
				t.Fatalf("warm memory or its route bookkeeping was omitted: %+v", input)
			}
		})
	}
}
