package routing

import (
	"path/filepath"
	"strings"
	"testing"
)

const roleLanes = `
	"opus":{"harness":"claude","model":"claude-opus-5-5","effort":"xhigh","note":"lead"},
	"sonnet":{"harness":"claude","model":"claude-sonnet-5-5","effort":"high","note":"implementation"},
	"fable":{"harness":"claude","model":"claude-fable-5-2","effort":"xhigh","note":"review"},
	"astra":{"harness":"codex","model":"gpt-6-astra","effort":"xhigh","note":"codex lead"},
	"pi-strong":{"harness":"pi","model":"provider/strong-model","effort":"high","note":"pi lead"},
	"pi-light":{"harness":"pi","model":"provider/light-model","effort":"medium","note":"pi implementation"}`

func roleTable(t *testing.T, roles string) Table {
	t.Helper()
	dir := t.TempDir()
	write(t, dir, `{"default_lane":"opus","lanes":{`+roleLanes+`},"roles":`+roles+`}`)
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

func laneNames(lanes []ExecutionLane) string {
	names := make([]string, 0, len(lanes))
	for _, lane := range lanes {
		names = append(names, lane.Name)
	}
	return strings.Join(names, ",")
}

func TestLaneTableReadsEachRoleWithItsFallbacks(t *testing.T) {
	// Arrange
	table := roleTable(t, `{"opus":{"implement":["sonnet","opus"],"review":["fable"],"verify":["fable","sonnet"],"fix":["opus"]}}`)
	lead := table.Lanes["opus"]

	// Act
	implement := table.RoleLanes(RoleImplement, lead)
	verify := table.RoleLanes(RoleVerify, lead)

	// Assert
	if laneNames(implement) != "sonnet,opus" || implement[0].Model != "claude-sonnet-5-5" || implement[0].Effort != "high" {
		t.Errorf("implement = %+v, want sonnet then its fallback opus", implement)
	}
	if laneNames(verify) != "fable,sonnet" {
		t.Errorf("verify = %+v, want fable then sonnet", verify)
	}
}

func TestLaneTableRefusesRolesAGoblinCannotRunOn(t *testing.T) {
	for _, test := range []struct{ name, roles, want string }{
		{"undefined lead", `{"nope":{"implement":["sonnet"]}}`, `roles for "nope", which is not a defined lane`},
		{"a lead role", `{"opus":{"lead":["fable"]}}`, `roles for "opus" name a lead, which is the lane itself`},
		{"unknown role", `{"opus":{"plan":["fable"]}}`, `roles for "opus" name "plan", which is not implement, review, verify or fix`},
		{"no lane", `{"opus":{"review":[]}}`, `role review for "opus" names no lane`},
		{"undefined lane", `{"opus":{"review":["fabel"]}}`, `role review for "opus" names "fabel", which is not a defined lane`},
		{"another harness", `{"opus":{"review":["astra"]}}`, `role review for "opus" names "astra" on codex: a role runs on its lead's harness, claude`},
		{"repeated lane", `{"opus":{"verify":["fable","fable"]}}`, `role verify for "opus" names "fable" twice`},
		{"an implement role on pi", `{"pi-strong":{"implement":["pi-light"]}}`, `role implement for "pi-strong": a pi goblin has no native subagent to hand a slice to: remove the role, and its lead implements its own work`},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			dir := t.TempDir()
			write(t, dir, `{"default_lane":"opus","lanes":{`+roleLanes+`},"roles":`+test.roles+`}`)
			policy, err := Load(dir)
			if err != nil {
				t.Fatalf("Load: %v, want roles checked only when a spawn routes", err)
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

func TestLaneTableRefusesRolesThatNeedAModelTheLaneDoesNotName(t *testing.T) {
	for _, test := range []struct{ name, lanes, want string }{
		{"role lane on the harness default", `"opus":{"harness":"claude","model":"claude-opus-5-5"},"plain":{"harness":"claude"}`, `role review for "opus" names "plain", which has no explicit model`},
		{"lead on the harness default", `"opus":{"harness":"claude","model":"default"},"plain":{"harness":"claude","model":"claude-fable-5-2"}`, `roles for "opus" need its own explicit model`},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			dir := t.TempDir()
			write(t, dir, `{"default_lane":"opus","lanes":{`+test.lanes+`},"roles":{"opus":{"review":["plain"]}}}`)
			policy, err := Load(dir)
			if err != nil {
				t.Fatal(err)
			}

			// Act
			_, err = policy.LaneTable()

			// Assert
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Errorf("err = %v, want %q", err, test.want)
			}
		})
	}
}

func TestARoleItsLaneDoesNotNameIsTheLeadsOwn(t *testing.T) {
	for _, test := range []struct{ name, policy, lead string }{
		{"a table without roles", `{"default_lane":"opus","lanes":{` + roleLanes + `},"subagents":{}}`, "opus"},
		{"a lane the roles do not name", `{"default_lane":"opus","lanes":{` + roleLanes + `},"roles":{"opus":{"review":["fable"]}}}`, "sonnet"},
		{"a role the lane does not name", `{"default_lane":"opus","lanes":{` + roleLanes + `},"roles":{"opus":{"review":["fable"]}}}`, "opus"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			dir := t.TempDir()
			write(t, dir, test.policy)
			policy, err := Load(dir)
			if err != nil {
				t.Fatal(err)
			}
			table, err := policy.LaneTable()
			if err != nil {
				t.Fatalf("LaneTable: %v, want the table to keep working", err)
			}
			lead := table.Lanes[test.lead]

			for _, role := range []string{RoleLead, RoleImplement, RoleVerify, RoleFix} {
				// Act
				lanes := table.RoleLanes(role, lead)

				// Assert
				if laneNames(lanes) != test.lead {
					t.Errorf("%s = %+v, want the lead %s itself", role, lanes, test.lead)
				}
			}
		})
	}
}

func TestChooseRoleFallsBackAlongTheRolesOwnLanes(t *testing.T) {
	// Arrange
	table := roleTable(t, `{"opus":{"implement":["sonnet","fable"]}}`)
	var checked []string
	usable := func(lane ExecutionLane) (bool, string) {
		checked = append(checked, lane.Name)
		if lane.Name == "sonnet" {
			return false, "claude-sonnet-5-5 at its start floor"
		}
		return true, ""
	}

	// Act
	choice, err := table.ChooseRole(RoleImplement, table.Lanes["opus"], usable)

	// Assert
	if err != nil || choice.Name != "fable" || choice.Wanted != "sonnet" || strings.Join(checked, ",") != "sonnet,fable" {
		t.Fatalf("choice = %+v checked = %v err = %v, want fable after sonnet was unusable", choice, checked, err)
	}
	if len(choice.Notes) != 1 || choice.Notes[0] != "sonnet: claude-sonnet-5-5 at its start floor" {
		t.Errorf("notes = %v, want what the passed-over lane showed", choice.Notes)
	}
}

func TestImplementationAndFixesEndOnTheLeadWhenNoLaneTheyNameCanRun(t *testing.T) {
	for _, role := range []string{RoleImplement, RoleFix} {
		t.Run(role, func(t *testing.T) {
			// Arrange
			table := roleTable(t, `{"opus":{"`+role+`":["sonnet","fable"]}}`)
			usable := func(lane ExecutionLane) (bool, string) {
				if lane.Name == "opus" {
					return true, ""
				}
				return false, lane.Model + " at its start floor"
			}

			// Act
			choice, err := table.ChooseRole(role, table.Lanes["opus"], usable)

			// Assert
			if err != nil || choice.Name != "opus" || choice.Wanted != "sonnet" {
				t.Fatalf("choice = %+v err = %v, want the lead opus after sonnet and fable", choice, err)
			}
			if want := "sonnet: claude-sonnet-5-5 at its start floor; fable: claude-fable-5-2 at its start floor"; strings.Join(choice.Notes, "; ") != want {
				t.Errorf("notes = %v, want %q", choice.Notes, want)
			}
		})
	}
}

func TestRoleLanesListOnlyTheLanesARoleWouldTry(t *testing.T) {
	for _, test := range []struct{ name, role, lanes, want string }{
		{"review leaves out the lead's model", RoleReview, `["opus","fable"]`, "fable"},
		{"verify leaves out the lead's model", RoleVerify, `["fable","opus","sonnet"]`, "fable,sonnet"},
		{"verify on the lead's model alone keeps it", RoleVerify, `["opus"]`, "opus"},
		{"implement ends on the lead", RoleImplement, `["sonnet"]`, "sonnet,opus"},
		{"fix that names the lead keeps its order", RoleFix, `["opus","sonnet"]`, "opus,sonnet"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			table := roleTable(t, `{"opus":{"`+test.role+`":`+test.lanes+`}}`)

			// Act
			lanes := table.RoleLanes(test.role, table.Lanes["opus"])

			// Assert
			if laneNames(lanes) != test.want {
				t.Errorf("lanes = %s, want %s", laneNames(lanes), test.want)
			}
		})
	}
}

func TestReviewAndVerificationRefuseTheLeadsOwnModel(t *testing.T) {
	for _, role := range []string{RoleReview, RoleVerify} {
		for _, test := range []struct {
			name, lanes, unusable, want, refusal string
		}{
			{name: "the lead's model is passed over", lanes: `["opus","fable"]`, want: "fable"},
			{name: "no other is usable", lanes: `["fable","opus"]`, unusable: "fable", refusal: "opus: " + role + " is kept off the lead's own model claude-opus-5-5"},
			{name: "the one lane they name is unusable", lanes: `["fable"]`, unusable: "fable", refusal: "no usable " + role + " lane"},
			{name: "every lane is the lead's model", lanes: `["opus"]`, want: "opus"},
		} {
			t.Run(role+" "+test.name, func(t *testing.T) {
				// Arrange
				table := roleTable(t, `{"opus":{"`+role+`":`+test.lanes+`}}`)
				usable := func(lane ExecutionLane) (bool, string) { return lane.Name != test.unusable, "" }

				// Act
				choice, err := table.ChooseRole(role, table.Lanes["opus"], usable)

				// Assert
				if test.refusal != "" {
					if err == nil || !strings.Contains(err.Error(), test.refusal) {
						t.Fatalf("choice = %+v err = %v, want a refusal saying %q", choice, err, test.refusal)
					}
					return
				}
				if err != nil || choice.Name != test.want {
					t.Fatalf("choice = %+v err = %v, want %s", choice, err, test.want)
				}
			})
		}
	}
}

func TestOtherRolesMayRunOnTheLeadsOwnModel(t *testing.T) {
	// Arrange
	table := roleTable(t, `{"opus":{"implement":["opus","sonnet"],"fix":["opus"]}}`)

	for _, role := range []string{RoleImplement, RoleFix} {
		// Act
		choice, err := table.ChooseRole(role, table.Lanes["opus"], nil)

		// Assert
		if err != nil || choice.Name != "opus" {
			t.Errorf("%s = %+v err = %v, want the lead's own lane it names first", role, choice, err)
		}
	}
}

// The shipped table leads on the strong tier and hands implementation to the
// lighter one, with verification on a model other than the lead's.
func TestShippedRolesLeadStrongAndImplementLighter(t *testing.T) {
	// Arrange
	policy, err := Load(filepath.Join("..", "..", "data"))
	if err != nil {
		t.Fatal(err)
	}
	table, err := policy.LaneTable()
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct{ lead, implement, review, verify, fix string }{
		{"build", "gpt-6.1-sol", "gpt-6-astra", "gpt-6-sol", "gpt-6-astra"},
		{"deep", "gpt-6-astra", "gpt-6-astra", "gpt-6-sol", "gpt-6-astra"},
		{"strong-claude", "claude-sonnet-5-5", "claude-fable-5-2", "claude-fable-5-2", "claude-opus-5-5"},
	} {
		t.Run(test.lead, func(t *testing.T) {
			lead := table.Lanes[test.lead]
			for role, want := range map[string]string{RoleImplement: test.implement, RoleReview: test.review, RoleVerify: test.verify, RoleFix: test.fix} {
				// Act
				choice, err := table.ChooseRole(role, lead, nil)

				// Assert
				if err != nil || choice.Model != want {
					t.Errorf("%s = %+v err = %v, want %s", role, choice, err, want)
				}
			}
		})
	}
}
