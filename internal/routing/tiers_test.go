package routing

import (
	"path/filepath"
	"strings"
	"testing"
)

const tierLanes = roleLanes + `,
	"haiku":{"harness":"claude","model":"haiku","effort":"medium","note":"small work"},
	"unranked":{"harness":"claude","model":"claude-other-1","effort":"high","note":"a model no tier names"},
	"plain":{"harness":"claude","note":"the harness default"}`

func tierTable(t *testing.T, tiers string) Table {
	t.Helper()
	dir := t.TempDir()
	write(t, dir, `{"default_lane":"opus","lanes":{`+tierLanes+`},"tiers":`+tiers+`}`)
	policy, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	table, err := policy.LaneTable()
	if err != nil {
		t.Fatalf("LaneTable: %v", err)
	}
	return table
}

func TestLaneTableRefusesTiersThatCannotRankAModel(t *testing.T) {
	for _, test := range []struct{ name, tiers, want string }{
		{"no lane", `{"claude":[]}`, `tiers for claude name no lane`},
		{"undefined lane", `{"claude":["fable","opsu"]}`, `tiers for claude name "opsu", which is not a defined lane`},
		{"another harness", `{"claude":["fable","astra"]}`, `tiers for claude name "astra", which runs on codex`},
		{"the harness default", `{"claude":["fable","plain"]}`, `tiers for claude name "plain", which has no explicit model`},
		{"one model twice", `{"claude":["fable","opus","fable"]}`, `tiers for claude name model claude-fable-5-2 twice, in "fable" and "fable": a tier is one model`},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			dir := t.TempDir()
			write(t, dir, `{"default_lane":"opus","lanes":{`+tierLanes+`},"tiers":`+test.tiers+`}`)
			policy, err := Load(dir)
			if err != nil {
				t.Fatalf("Load: %v, want tiers checked only when a table is built", err)
			}

			// Act
			_, err = policy.LaneTable()

			// Assert
			if want := "routing: " + filepath.Join(dir, FileName) + ": " + test.want; err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("err = %v, want %q", err, want)
			}
		})
	}
}

func TestEscalationAfterAFailedVerificationMovesTheRoleUpOneTier(t *testing.T) {
	// Arrange
	table := tierTable(t, `{"claude":["fable","opus","sonnet","haiku"]}`)

	for _, test := range []struct{ name, current, lead, want string }{
		{"one tier up is the lead's", "sonnet", "opus", "opus"},
		{"one tier up is below the lead", "haiku", "fable", "sonnet"},
		{"one tier up is the next below the lead", "sonnet", "fable", "opus"},
		{"the lead's own tier moves above it", "opus", "opus", "fable"},
		{"the top tier stays", "fable", "opus", "fable"},
		{"the top tier stays under a lead no tier ranks", "fable", "unranked", "fable"},
		{"a model no tier ranks goes to the lead", "unranked", "opus", "opus"},
		{"a lead no tier ranks leaves the move to the tiers", "sonnet", "unranked", "opus"},
		{"a harness with no tiers goes to the lead", "pi-light", "pi-strong", "pi-strong"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			next := table.EscalateRole(table.Lanes[test.current], table.Lanes[test.lead])

			// Assert
			if next.Name != test.want {
				t.Errorf("EscalateRole(%s, lead %s) = %+v, want %s", test.current, test.lead, next, test.want)
			}
		})
	}
}

func TestATableWithoutTiersHandsAnEscalatedRoleToTheLead(t *testing.T) {
	// Arrange
	table := roleTable(t, `{"opus":{"implement":["sonnet"]}}`)

	// Act
	next := table.EscalateRole(table.Lanes["sonnet"], table.Lanes["opus"])

	// Assert
	if next != table.Lanes["opus"] {
		t.Errorf("EscalateRole = %+v, want the lead opus", next)
	}
}

// The shipped tiers put each shipped implement worker one tier below its
// lead, so a failed verification hands implementation back to the lead.
func TestShippedTiersHandAFailedImplementationBackToTheLead(t *testing.T) {
	// Arrange
	policy, err := Load(filepath.Join("..", "..", "data"))
	if err != nil {
		t.Fatal(err)
	}
	table, err := policy.LaneTable()
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"build", "strong-claude"} {
		t.Run(name, func(t *testing.T) {
			lead := table.Lanes[name]
			implement, err := table.ChooseRole(RoleImplement, lead, nil)
			if err != nil || implement.ExecutionLane == lead {
				t.Fatalf("implement = %+v err = %v, want a worker lighter than the lead", implement, err)
			}

			// Act
			next := table.EscalateRole(implement.ExecutionLane, lead)

			// Assert
			if next != lead {
				t.Errorf("EscalateRole(%s) = %+v, want the lead %s", implement.Name, next, name)
			}
		})
	}
}
