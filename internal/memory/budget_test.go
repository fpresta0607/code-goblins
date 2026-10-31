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

func TestMemoryReportCountsCatalogRoutesAndPreservesSourceHashes(t *testing.T) {
	dir, _ := catalogFixture(t)
	report, err := MemoryReport(dir, referenceDate)
	if err != nil || report.IsLegacy || report.Bytes <= 0 || len(report.Inputs) != 1 {
		t.Fatalf("warm report=%+v error=%v", report, err)
	}
	if report.EstimatedTokens != Tokens(report.Bytes) || report.IsOverBudget || len(report.Inputs[0].SHA256) != 64 {
		t.Fatalf("warm accounting lacks complete emitted memory: %+v", report)
	}
}
