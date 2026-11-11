package gatetest

import (
	"slices"
	"testing"
)

// A gate step inherits the goblin pane's fleet variables and the gate agent's
// mark in any spelling Windows accepts, and every one of them is dropped
// while the rest stay.
func TestEnvironmentDropsTheFleetVariablesInAnySpelling(t *testing.T) {
	// Arrange
	env := []string{"PATH=C:\\bin", "GOBLINS_HOME=C:\\fleet", "ceo_state_override=C:\\fleet\\state", "Ceo_Home=C:\\other", "GOBLINS_HOMELY=kept", "no_mistakes_gate=1", "GOFLAGS=-mod=mod"}

	// Act
	kept := Environment(env)

	// Assert
	if want := []string{"PATH=C:\\bin", "GOBLINS_HOMELY=kept", "GOFLAGS=-mod=mod"}; !slices.Equal(kept, want) {
		t.Errorf("Environment = %q, want %q", kept, want)
	}
}
