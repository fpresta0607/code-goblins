package routing

import (
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

func TestChooseEscalatesOrdinaryWorkAndSaysWhy(t *testing.T) {
	usable := func(l ExecutionLane) (bool, string) {
		if l.Name == "build" {
			return false, "claude model:opus exhausted_now, resets 2026-09-23T09:00:00Z"
		}
		return true, "claude all_models 97% remaining, runway through_reset"
	}
	c, err := Choose(Assessment{Class: Implementation, Risk: "normal"}, fleetTable(), usable)
	if err != nil {
		t.Fatalf("Choose: %v", err)
	}
	if c.Name != "deep" || c.Wanted != "build" || c.Model != "fable" {
		t.Errorf("choice = %+v, want deep used with build wanted", c)
	}
	want := []string{
		"build: claude model:opus exhausted_now, resets 2026-09-23T09:00:00Z",
		"deep: claude all_models 97% remaining, runway through_reset",
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
	for _, name := range []string{"build", "deep"} {
		if !strings.Contains(err.Error(), name+": claude exhausted_now") {
			t.Errorf("err = %v, want it to name lane %s", err, name)
		}
	}
	if !strings.HasPrefix(err.Error(), "no usable lane: ") {
		t.Errorf("err = %v, want a refusal", err)
	}
}

func TestChooseTriesOnlyCapabilitySafeLanes(t *testing.T) {
	for _, test := range []struct {
		name       string
		assessment Assessment
		want       []string
	}{
		{"mechanical", Assessment{Class: Mechanical}, []string{"mechanical", "build", "deep"}},
		{"scout", Assessment{Class: Scout}, []string{"scout", "build", "deep"}},
		{"ordinary", Assessment{Class: Implementation}, []string{"build", "deep"}},
		{"high risk", Assessment{Class: Debug, Risk: "high"}, []string{"deep"}},
		{"security", Assessment{Class: Security}, []string{"deep"}},
		{"architecture", Assessment{Class: Architecture}, []string{"deep"}},
		{"migration", Assessment{Class: Migration}, []string{"deep"}},
		{"retry", Assessment{Class: Mechanical, Attempts: 2}, []string{"deep"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			table := fleetTable()
			table.Lanes["aaa-light"] = ExecutionLane{Name: "aaa-light", Harness: "codex", Model: "arbitrary-model"}
			var tried []string
			// Act
			choice, err := Choose(test.assessment, table, func(lane ExecutionLane) (bool, string) {
				tried = append(tried, lane.Name)
				return lane.Name == "aaa-light", "exhausted_now, resets tomorrow"
			})
			// Assert
			if err == nil || !reflect.DeepEqual(tried, test.want) {
				t.Fatalf("choice=%+v err=%v tried=%v, want refusal after %v", choice, err, tried, test.want)
			}
			if len(choice.Notes) != len(test.want) || strings.Contains(err.Error(), "aaa-light") {
				t.Fatalf("refusal includes evidence for untried lanes: %+v, %v", choice, err)
			}
		})
	}
}

func TestChooseMechanicalFallsBackToDefaultBeforeEscalation(t *testing.T) {
	// Arrange
	var tried []string
	// Act
	choice, err := Choose(Assessment{Class: Mechanical}, fleetTable(), func(lane ExecutionLane) (bool, string) {
		tried = append(tried, lane.Name)
		return lane.Name == "build", "quota evidence"
	})
	// Assert
	if err != nil || choice.Name != "build" || choice.Wanted != "mechanical" || !reflect.DeepEqual(tried, []string{"mechanical", "build"}) {
		t.Fatalf("choice=%+v err=%v tried=%v", choice, err, tried)
	}
}

func TestChooseRefusesHighRiskWithoutAnEscalationLane(t *testing.T) {
	for _, usable := range []Usable{nil, func(ExecutionLane) (bool, string) { return true, "available" }} {
		// Arrange
		table := fleetTable()
		table.EscalateTo = ""
		// Act
		_, err := Choose(Assessment{Class: Security, Risk: "high"}, table, usable)
		// Assert
		if err == nil || !strings.Contains(err.Error(), "escalat") {
			t.Fatalf("Choose=%v, want refusal without a configured escalation lane", err)
		}
	}
}

func TestChooseExecutionStaysOnTheDefaultWithoutAnEscalationLane(t *testing.T) {
	lanes := map[string]ExecutionLane{"build": {Name: "build", Harness: "claude"}}
	if l := ChooseExecution(Assessment{Class: Security, Risk: "high"}, lanes, "build", ""); l.Name != "build" || l.Harness != "claude" {
		t.Errorf("lane = %+v, want the default rather than an empty escalation", l)
	}
}
