package routing

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func fleetTable() Table {
	return Table{
		Lanes: map[string]ExecutionLane{
			"deep":       {Name: "deep", Harness: "claude", Model: "fable", Effort: "xhigh"},
			"build":      {Name: "build", Harness: "claude", Model: "opus", Effort: "high"},
			"mechanical": {Name: "mechanical", Harness: "claude", Model: "sonnet", Effort: "medium"},
			"scout":      {Name: "scout", Harness: "claude", Model: "opus", Effort: "xhigh"},
		},
		DefaultLane: "build",
		EscalateTo:  "deep",
		Source:      "fleet table",
	}
}

func TestChooseNamesTheLaneAClassAsksFor(t *testing.T) {
	cases := []struct {
		a    Assessment
		want string
	}{
		{Assessment{Class: Implementation, Risk: "normal"}, "build"},
		{Assessment{Class: Debug, Risk: "normal"}, "build"},
		{Assessment{Class: Scout, Risk: "normal"}, "scout"},
		{Assessment{Class: Mechanical, Risk: "normal"}, "mechanical"},
		{Assessment{Class: Security, Risk: "high"}, "deep"},
		{Assessment{Class: Migration, Risk: "high"}, "deep"},
		{Assessment{Class: Rescue, Risk: "high"}, "deep"},
		{Assessment{Class: Implementation, Risk: "normal", Attempts: 2}, "deep"},
	}
	for _, test := range cases {
		c, err := Choose(test.a, fleetTable(), nil)
		if err != nil {
			t.Fatalf("Choose(%+v): %v", test.a, err)
		}
		if c.Name != test.want || c.Wanted != test.want {
			t.Errorf("Choose(%+v) = %s (wanted %s), want %s", test.a, c.Name, c.Wanted, test.want)
		}
	}
}

func TestChooseFallsToTheNearestUsableLaneAndSaysWhy(t *testing.T) {
	usable := func(l ExecutionLane) (bool, string) {
		if l.Name == "deep" {
			return false, "claude model:fable exhausted_now, resets 2026-09-23T09:00:00Z"
		}
		return true, "claude all_models 97% remaining, runway through_reset"
	}
	c, err := Choose(Assessment{Class: Security, Risk: "high"}, fleetTable(), usable)
	if err != nil {
		t.Fatalf("Choose: %v", err)
	}
	if c.Name != "build" || c.Wanted != "deep" || c.Model != "opus" {
		t.Errorf("choice = %+v, want build used with deep wanted", c)
	}
	want := []string{
		"deep: claude model:fable exhausted_now, resets 2026-09-23T09:00:00Z",
		"build: claude all_models 97% remaining, runway through_reset",
	}
	if !reflect.DeepEqual(c.Notes, want) {
		t.Errorf("notes = %q, want every lane tried, in order", c.Notes)
	}
}

func TestChooseRefusesWhenNoLaneIsUsable(t *testing.T) {
	usable := func(l ExecutionLane) (bool, string) { return false, l.Harness + " exhausted_now" }
	_, err := Choose(Assessment{Class: Implementation, Risk: "normal"}, fleetTable(), usable)
	if err == nil {
		t.Fatal("Choose dispatched into a wall")
	}
	for _, name := range []string{"build", "deep", "mechanical", "scout"} {
		if !strings.Contains(err.Error(), name+": claude exhausted_now") {
			t.Errorf("err = %v, want it to name lane %s", err, name)
		}
	}
	if !strings.HasPrefix(err.Error(), "no usable lane: ") {
		t.Errorf("err = %v, want a refusal", err)
	}
}

func TestChooseNeverChecksAnExplicitHarness(t *testing.T) {
	usable := func(ExecutionLane) (bool, string) { return false, "would refuse" }
	c, err := Choose(Assessment{Class: Security, Risk: "high", ExplicitHarness: "codex", ExplicitModel: "gpt-5"}, fleetTable(), usable)
	if err != nil {
		t.Fatalf("an explicit harness was refused: %v", err)
	}
	if c.Name != "explicit" || c.Harness != "codex" || c.Model != "gpt-5" || len(c.Notes) != 0 {
		t.Errorf("choice = %+v, want the operator's harness untouched", c)
	}
}

func TestFallbackOrderIsWantedDefaultEscalationThenTheRestByName(t *testing.T) {
	got := fallbackOrder("mechanical", fleetTable())
	want := []string{"mechanical", "build", "deep", "scout"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("fallbackOrder = %v, want %v", got, want)
	}
	if got := fallbackOrder("deep", fleetTable()); !reflect.DeepEqual(got, []string{"deep", "build", "mechanical", "scout"}) {
		t.Errorf("fallbackOrder from deep = %v", got)
	}
}

func TestChooseExecutionStaysOnTheDefaultWithoutAnEscalationLane(t *testing.T) {
	lanes := map[string]ExecutionLane{"build": {Name: "build", Harness: "claude"}}
	if l := ChooseExecution(Assessment{Class: Security, Risk: "high"}, lanes, "build", ""); l.Name != "build" || l.Harness != "claude" {
		t.Errorf("lane = %+v, want the default rather than an empty escalation", l)
	}
}

var _ = errors.New
