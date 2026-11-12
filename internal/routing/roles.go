package routing

import (
	"fmt"
	"slices"
	"strings"
)

// The roles a goblin's work splits into. The lead plans, decomposes,
// integrates and decides: it is the lane the goblin runs on, with that lane's
// fallbacks. The other roles are named per lane in the policy's roles, each
// an ordered list of lanes, the role's own first and then its fallbacks.
const (
	RoleLead      = "lead"
	RoleImplement = "implement"
	RoleReview    = "review"
	RoleVerify    = "verify"
	RoleFix       = "fix"
)

// Roles lists every role in the order route and doctor print them.
var Roles = []string{RoleLead, RoleImplement, RoleReview, RoleVerify, RoleFix}

// roleTable checks the policy's roles and resolves each lane they name. A
// role runs on its lead's harness, because its native helpers and the gate's
// agents start there, and on an explicit model, because review and
// verification are kept apart from the lead's own model.
func (p Policy) roleTable() (map[string]map[string][]ExecutionLane, error) {
	if len(p.Roles) == 0 {
		return nil, nil
	}
	table := make(map[string]map[string][]ExecutionLane, len(p.Roles))
	for leadName, roles := range p.Roles {
		lead, ok := p.Lanes[leadName]
		if !ok {
			return nil, fmt.Errorf("routing: %s: roles for %q, which is not a defined lane", p.Path, leadName)
		}
		if !hasExplicitModel(lead.Model) {
			return nil, fmt.Errorf("routing: %s: roles for %q need its own explicit model, which its review and verification are kept apart from", p.Path, leadName)
		}
		table[leadName] = make(map[string][]ExecutionLane, len(roles))
		for role, names := range roles {
			switch role {
			case RoleImplement, RoleReview, RoleVerify, RoleFix:
			case RoleLead:
				return nil, fmt.Errorf("routing: %s: roles for %q name a lead, which is the lane itself: set the lane's own harness, model and effort, and its fallbacks in fallbacks", p.Path, leadName)
			default:
				return nil, fmt.Errorf("routing: %s: roles for %q name %q, which is not implement, review, verify or fix", p.Path, leadName, role)
			}
			if len(names) == 0 {
				return nil, fmt.Errorf("routing: %s: role %s for %q names no lane", p.Path, role, leadName)
			}
			// A lead hands an implementation slice to a native subagent, and
			// Pi has none, so a Pi lead implements its own work.
			if role == RoleImplement && lead.Harness == "pi" {
				return nil, fmt.Errorf("routing: %s: role implement for %q: a pi goblin has no native subagent to hand a slice to, so its lead implements", p.Path, leadName)
			}
			for index, name := range names {
				lane, ok := p.Lanes[name]
				switch {
				case !ok:
					return nil, fmt.Errorf("routing: %s: role %s for %q names %q, which is not a defined lane", p.Path, role, leadName, name)
				case lane.Harness != lead.Harness:
					return nil, fmt.Errorf("routing: %s: role %s for %q names %q on %s: a role runs on its lead's harness, %s", p.Path, role, leadName, name, lane.Harness, lead.Harness)
				case !hasExplicitModel(lane.Model):
					return nil, fmt.Errorf("routing: %s: role %s for %q names %q, which has no explicit model", p.Path, role, leadName, name)
				case slices.Contains(names[:index], name):
					return nil, fmt.Errorf("routing: %s: role %s for %q names %q twice", p.Path, role, leadName, name)
				}
				table[leadName][role] = append(table[leadName][role], ExecutionLane{Name: name, Harness: lane.Harness, Model: lane.Model, Effort: lane.Effort, Note: lane.Note})
			}
		}
	}
	return table, nil
}

func hasExplicitModel(model string) bool {
	return model != "" && model != "default"
}

// RoleLanes returns the lanes a role tries, in order, for a goblin led on
// lead. A role its lane does not name is the lead's own work, as every role
// was before the table named roles, and implementation and fixes end on the
// lead when none of the lanes they name can run. Review and verification are
// kept apart from the lead's own model: while their lanes name another model,
// a lane on the lead's model is left out, so the role is refused rather than
// run on it. They take the lead's model only when every lane they name is
// that model.
func (t Table) RoleLanes(role string, lead ExecutionLane) []ExecutionLane {
	lanes, ok := t.Roles[lead.Name][role]
	if !ok {
		return []ExecutionLane{lead}
	}
	if role == RoleReview || role == RoleVerify {
		apart := slices.DeleteFunc(slices.Clone(lanes), func(lane ExecutionLane) bool { return SameModel(lead.Harness, lead.Model, lane.Model) })
		if len(apart) == 0 {
			return lanes
		}
		return apart
	}
	if slices.ContainsFunc(lanes, func(lane ExecutionLane) bool { return lane.Name == lead.Name }) {
		return lanes
	}
	return append(slices.Clone(lanes), lead)
}

// ChooseRole picks the first usable lane RoleLanes returns, noting first each
// lane it left out for being on the lead's own model.
func (t Table) ChooseRole(role string, lead ExecutionLane, usable Usable) (Choice, error) {
	lanes := t.RoleLanes(role, lead)
	choice := Choice{ExecutionLane: lanes[0], Wanted: lanes[0].Name}
	for _, lane := range t.Roles[lead.Name][role] {
		if !slices.Contains(lanes, lane) {
			choice.Notes = append(choice.Notes, lane.Name+": "+role+" is kept off the lead's own model "+lead.Model)
		}
	}
	for _, lane := range lanes {
		isUsable, note := true, ""
		if usable != nil {
			isUsable, note = usable(lane)
		}
		if note != "" {
			choice.Notes = append(choice.Notes, lane.Name+": "+note)
		}
		if isUsable {
			choice.ExecutionLane = lane
			return choice, nil
		}
	}
	return choice, fmt.Errorf("no usable %s lane: %s", role, strings.Join(choice.Notes, "; "))
}
