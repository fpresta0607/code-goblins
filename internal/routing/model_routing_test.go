package routing

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRoutingUsesComplexityBeforeTheDefault(t *testing.T) {
	for _, test := range []struct{ task, want string }{
		{"Make a narrow single-file change to the date label.", "mechanical"},
		{"Implement a complex cross-cutting scheduler redesign.", "deep"},
		{"Make a narrow security fix for access control.", "deep"},
		{"Add a settings page.", "build"},
		{"## Delivery\nkind: scout\n## Task\nInvestigate a security vulnerability.", "deep"},
	} {
		t.Run(test.want+test.task, func(t *testing.T) {
			choice, err := Choose(Classify("## Task\n"+test.task), fleetTable(), nil)
			if err != nil || choice.Name != test.want {
				t.Fatalf("choice=%+v err=%v, want %s", choice, err, test.want)
			}
		})
	}
}

func TestVerificationHelperUsesAnotherModelEvenWithoutQuotaEvidence(t *testing.T) {
	policy, err := Load(filepath.Join("..", "..", "data"))
	if err != nil {
		t.Fatal(err)
	}
	table, err := policy.LaneTable()
	if err != nil {
		t.Fatal(err)
	}
	assessment := Classify("Verify the completed implementation and test results.")
	assessment.CheckedHarness, assessment.CheckedModel = "codex", "gpt-6-sol"
	choice, err := Choose(assessment, table, nil)
	if err != nil || choice.Model != "gpt-6-astra" {
		t.Fatalf("choice=%+v err=%v", choice, err)
	}
}

func TestVerificationHelperAcceptsUniformConfiguredModel(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, `{"default_lane":"verification","lanes":{"verification":{"harness":"codex","model":"selected-model","effort":"xhigh"}}}`)
	policy, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	table, err := policy.LaneTable()
	if err != nil {
		t.Fatal(err)
	}
	assessment := Classify("Verify the completed implementation.")
	assessment.CheckedHarness, assessment.CheckedModel = "codex", "selected-model"
	choice, err := Choose(assessment, table, nil)
	if err != nil || choice.Model != "selected-model" {
		t.Fatalf("choice=%+v err=%v, want the configured model", choice, err)
	}
}

func TestVerificationHelperUsesSameModelWhenAlternativeIsUnavailable(t *testing.T) {
	table := Table{DefaultLane: "same", Fallbacks: map[string][]string{"same": {"other"}}, Lanes: map[string]ExecutionLane{
		"same":  {Name: "same", Harness: "codex", Model: "selected-model"},
		"other": {Name: "other", Harness: "codex", Model: "other-model"},
	}}
	assessment := Assessment{Class: Verification, CheckedHarness: "codex", CheckedModel: "selected-model"}
	checked := []string{}
	choice, err := Choose(assessment, table, func(lane ExecutionLane) (bool, string) {
		checked = append(checked, lane.Name)
		return lane.Name == "same", "quota checked"
	})
	if err != nil || choice.Name != "same" || strings.Join(checked, ",") != "other,same" {
		t.Fatalf("choice=%+v checked=%v err=%v", choice, checked, err)
	}
}

func TestVerificationHelperKeepsExplicitModelOnItsHarness(t *testing.T) {
	table := Table{DefaultLane: "same", Fallbacks: map[string][]string{"same": {"other"}}, Lanes: map[string]ExecutionLane{
		"same":  {Name: "same", Harness: "codex", Model: "selected-model"},
		"other": {Name: "other", Harness: "claude", Model: "other-model"},
	}}
	assessment := Assessment{Class: Verification, CheckedHarness: "codex", CheckedModel: "selected-model", ExplicitModel: "selected-model"}
	choice, err := Choose(assessment, table, func(lane ExecutionLane) (bool, string) {
		return lane.Name == "same", "quota checked"
	})
	if err != nil || choice.Name != "same" {
		t.Fatalf("choice=%+v err=%v", choice, err)
	}
}

func TestVerificationHelperDoesNotReplaceExplicitModel(t *testing.T) {
	table := Table{DefaultLane: "same", Fallbacks: map[string][]string{"same": {"other"}}, Lanes: map[string]ExecutionLane{
		"same":  {Name: "same", Harness: "codex", Model: "selected-model"},
		"other": {Name: "other", Harness: "codex", Model: "other-model"},
	}}
	assessment := Assessment{Class: Verification, CheckedHarness: "codex", CheckedModel: "selected-model", ExplicitModel: "selected-model"}
	choice, err := Choose(assessment, table, func(lane ExecutionLane) (bool, string) { return true, "quota checked" })
	if err != nil || choice.Model != "selected-model" {
		t.Fatalf("choice=%+v err=%v", choice, err)
	}
}

func TestVerificationHelperRejectsUniformModelWhenQuotaIsUnavailable(t *testing.T) {
	table := Table{DefaultLane: "same", Lanes: map[string]ExecutionLane{
		"same": {Name: "same", Harness: "codex", Model: "selected-model"},
	}}
	assessment := Assessment{Class: Verification, CheckedHarness: "codex", CheckedModel: "selected-model"}
	_, err := Choose(assessment, table, func(ExecutionLane) (bool, string) { return false, "quota exhausted" })
	if err == nil || !strings.Contains(err.Error(), "quota exhausted") {
		t.Fatalf("verification ignored quota: %v", err)
	}
}

func TestVerificationClassifiesTheTaskRatherThanItsChecklist(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		for _, test := range []struct {
			brief          string
			class          TaskClass
			isVerification bool
		}{
			{"## Task\nImplement a settings page.\n## Acceptance criteria\nVerify the page works.\n", Implementation, false},
			{"## Task - Settings\nImplement a settings page.\n2. Verify the page works.\n", Implementation, false},
			{"## Task\nFix the login regression.\nVerify it in the browser.\n", Debug, false},
			{"## Task\nVerify the security changes.\n## Acceptance criteria\nReturn evidence.\n", Security, true},
			{"## Task - Verification\nValidate the completed changes.\n", Verification, true},
			{"## Task\nIndependently verify the completed changes.\n", Verification, true},
			{"## Task\n1. Verify the completed changes.\n", Verification, true},
			{"## Task\nImplement independent verification for the export.\n", Implementation, false},
		} {
			brief := strings.ReplaceAll(test.brief, "\n", newline)
			if got := Classify(brief); got.IsVerification != test.isVerification || got.Class != test.class {
				t.Errorf("Classify(%q) = %+v, want %s with verification=%v", brief, got, test.class, test.isVerification)
			}
		}
	}
}

func TestRoutingUsesDeclaredStrongFallbackAcrossHarnesses(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, `{"default_lane":"build","escalate_to":"deep","fallbacks":{"deep":["strong-claude"]},"lanes":{
		"build":{"harness":"codex","model":"gpt-6-astra"},
		"deep":{"harness":"codex","model":"gpt-6-astra"},
		"strong-claude":{"harness":"claude","model":"opus"}}}`)
	policy, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	table, err := policy.LaneTable()
	if err != nil {
		t.Fatal(err)
	}
	choice, err := Choose(Classify("Fix a security vulnerability."), table, func(lane ExecutionLane) (bool, string) {
		return lane.Harness == "claude", "allowance floor"
	})
	if err != nil || choice.Harness != "claude" || choice.Wanted != "deep" {
		t.Fatalf("choice=%+v err=%v, want declared strong fallback", choice, err)
	}
}

func TestRoutingRejectsUndefinedFallback(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, `{"default_lane":"build","lanes":{"build":{"harness":"codex"}},"fallbacks":{"build":["missing"]}}`)
	policy, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = policy.LaneTable()
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("undefined fallback accepted: %v", err)
	}
}

func TestShippedRoutingUsesStandingDefaultAndSmallNarrowLane(t *testing.T) {
	policy, err := Load(filepath.Join("..", "..", "data"))
	if err != nil {
		t.Fatal(err)
	}
	table, err := policy.LaneTable()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ task, model, effort string }{
		{"Add a settings page.", "gpt-6-astra", "xhigh"},
		{"Rename the label to Save.", "gpt-6-luna", "medium"},
		{"Fix access control.", "gpt-6-astra", "xhigh"},
	} {
		choice, err := Choose(Classify(test.task), table, nil)
		if err != nil || choice.Model != test.model || choice.Effort != test.effort {
			t.Errorf("%s: choice=%+v err=%v", test.task, choice, err)
		}
	}
}
