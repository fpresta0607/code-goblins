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

func TestVerificationClassifiesTheTaskRatherThanItsChecklist(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		brief := strings.ReplaceAll("## Task\nImplement a settings page.\n## Acceptance criteria\nVerify the page works.\n", "\n", newline)
		if got := Classify(brief); got.IsVerification || got.Class != Implementation {
			t.Errorf("implementation classified as verification: %+v", got)
		}
		brief = strings.ReplaceAll("## Task\nVerify the security changes.\n## Acceptance criteria\nReturn evidence.\n", "\n", newline)
		if got := Classify(brief); !got.IsVerification || got.Class != Security {
			t.Errorf("security verifier lost its job: %+v", got)
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
