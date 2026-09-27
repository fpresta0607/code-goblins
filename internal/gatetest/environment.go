package gatetest

import "strings"

// fleetVariables point cfo at the running fleet. A gate step inherits them
// from the goblin's pane, and a test that runs cfo with them writes into the
// live fleet, as a test step's cfo notify once did.
var fleetVariables = []string{"CFO_HOME", "CFO_STATE_OVERRIDE"}

// Environment is env without the fleet's variables, compared without regard
// to case as Windows does.
func Environment(env []string) []string {
	kept := make([]string, 0, len(env))
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		fleet := false
		for _, variable := range fleetVariables {
			fleet = fleet || strings.EqualFold(name, variable)
		}
		if !fleet {
			kept = append(kept, entry)
		}
	}
	return kept
}
