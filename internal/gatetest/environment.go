package gatetest

import "strings"

// excludedVariables are kept from the step's go. GOBLINS_HOME and
// GOBLINS_STATE_OVERRIDE point goblins at the running fleet: a gate step inherits
// them from the goblin's pane, and a test that runs goblins with them writes into
// the live fleet, as a test step's goblins notify once did. NO_MISTAKES_GATE
// marks a gate agent's process, under which every goblins hook does nothing, so
// the step's hook tests would test nothing. GOBLINS_TEST_EVIDENCE picks the
// step's own default level, and a test of that default would read the gate's.
var excludedVariables = []string{"GOBLINS_HOME", "GOBLINS_STATE_OVERRIDE", "NO_MISTAKES_GATE", "GOBLINS_TEST_EVIDENCE"}

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
