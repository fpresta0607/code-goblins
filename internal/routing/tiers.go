package routing

import (
	"fmt"
	"slices"
)

// tierTable checks the policy's tiers and resolves each lane they name. A
// harness's tiers run strongest first, one lane for each model, and a role
// moves up them by rule once its work failed verification. A harness the
// tiers do not list has no order to move along.
func (p Policy) tierTable() (map[string][]ExecutionLane, error) {
	if len(p.Tiers) == 0 {
		return nil, nil
	}
	table := make(map[string][]ExecutionLane, len(p.Tiers))
	for harness, names := range p.Tiers {
		if len(names) == 0 {
			return nil, fmt.Errorf("routing: %s: tiers for %s name no lane", p.Path, harness)
		}
		for _, name := range names {
			lane, ok := p.Lanes[name]
			switch {
			case !ok:
				return nil, fmt.Errorf("routing: %s: tiers for %s name %q, which is not a defined lane", p.Path, harness, name)
			case lane.Harness != harness:
				return nil, fmt.Errorf("routing: %s: tiers for %s name %q, which runs on %s", p.Path, harness, name, lane.Harness)
			case !hasExplicitModel(lane.Model):
				return nil, fmt.Errorf("routing: %s: tiers for %s name %q, which has no explicit model", p.Path, harness, name)
			}
			for _, stronger := range table[harness] {
				if SameModel(harness, stronger.Model, lane.Model) {
					return nil, fmt.Errorf("routing: %s: tiers for %s name model %s twice, in %q and %q: a tier is one model", p.Path, harness, lane.Model, stronger.Name, name)
				}
			}
			table[harness] = append(table[harness], ExecutionLane{Name: name, Harness: lane.Harness, Model: lane.Model, Effort: lane.Effort, Note: lane.Note})
		}
	}
	return table, nil
}

// EscalateRole is the lane a role moves to once its work failed verification:
// the next stronger tier on its harness. When that tier is the lead's own,
// the role becomes the lead's own work, and so does a role whose model its
// harness's tiers do not rank. A role on its harness's top tier stays where
// it is.
func (t Table) EscalateRole(current, lead ExecutionLane) ExecutionLane {
	tiers := t.Tiers[current.Harness]
	rank := func(lane ExecutionLane) int {
		return slices.IndexFunc(tiers, func(tier ExecutionLane) bool { return SameModel(lane.Harness, tier.Model, lane.Model) })
	}
	from := rank(current)
	switch {
	case from < 0:
		return lead
	case from == 0:
		return current
	case from-1 == rank(lead):
		return lead
	}
	return tiers[from-1]
}
