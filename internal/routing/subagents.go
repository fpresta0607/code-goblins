package routing

import (
	"fmt"
	"strings"
)

// SubagentRoutes resolves native roles on the parent's harness. Verification
// must name a known different model, even when the parent used an alias.
func (p Policy) SubagentRoutes(harness, parentModel string) (map[string]ExecutionLane, error) {
	roles, ok := p.Subagents[harness]
	if !ok {
		return nil, nil
	}
	if parentModel == "" || parentModel == "default" {
		return nil, fmt.Errorf("routing: an explicit parent model is required for independent verification on %s", harness)
	}
	table, err := p.LaneTable()
	if err != nil {
		return nil, err
	}
	routes := make(map[string]ExecutionLane, 3)
	for _, role := range []string{"search", "review", "verify"} {
		for _, name := range roles[role] {
			lane, ok := table.Lanes[name]
			if !ok || lane.Harness != harness || lane.Model == "" || lane.Model == "default" {
				return nil, fmt.Errorf("routing: subagent %s/%s lane %q must name an explicit model on %s", harness, role, name, harness)
			}
			if role == "verify" && SameModel(harness, parentModel, lane.Model) {
				continue
			}
			routes[role] = lane
			break
		}
		if _, ok := routes[role]; !ok {
			return nil, fmt.Errorf("routing: no %s subagent model on %s independent of %s", role, harness, parentModel)
		}
	}
	return routes, nil
}

func SameModel(harness, left, right string) bool {
	left, right = strings.ToLower(left), strings.ToLower(right)
	if harness == "claude" {
		left = strings.TrimSuffix(strings.TrimPrefix(left, "claude-"), "[1m]")
		right = strings.TrimSuffix(strings.TrimPrefix(right, "claude-"), "[1m]")
	}
	return left == right || strings.HasPrefix(left, right+"-") || strings.HasPrefix(right, left+"-")
}
