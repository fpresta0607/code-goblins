package gatetest

import "strings"

// excludedVariables are kept from the step's go. CFO_HOME and
// CFO_STATE_OVERRIDE point cfo at the running fleet: a gate step inherits
// them from the goblin's pane, and a test that runs cfo with them writes into
// the live fleet, as a test step's cfo notify once did. NO_MISTAKES_GATE
// marks a gate agent's process, under which every cfo hook does nothing, so
// the step's hook tests would test nothing.
var excludedVariables = []string{"CFO_HOME", "CFO_STATE_OVERRIDE", "NO_MISTAKES_GATE"}

// Environment is env without the excluded variables, compared without
// regard to case as Windows does.
func Environment(env []string) []string {
	kept := make([]string, 0, len(env))
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		excluded := false
		for _, variable := range excludedVariables {
			excluded = excluded || strings.EqualFold(name, variable)
		}
		if !excluded {
			kept = append(kept, entry)
		}
	}
	return kept
}
