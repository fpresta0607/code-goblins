package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/cutover/cutovertest"
	"github.com/fpresta0607/code-goblins/internal/installbase"
)

// A home outside the standard folder that a release before the rename named
// only by its own home setting is the home the setup's plan updates, kept
// where it is, as the install keeps it.
func TestThePlanKeepsAHomeOnlyTheOldHomeSettingNames(t *testing.T) {
	// Arrange
	machine := t.TempDir()
	local := filepath.Join(machine, "Local")
	root := filepath.Join(machine, "my-home")
	for _, folder := range []string{filepath.Join(root, "state"), local} {
		if err := os.MkdirAll(folder, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	data, err := json.Marshal(map[string]string{cutovertest.HomeSetting: root})
	if err != nil {
		t.Fatal(err)
	}
	environment := filepath.Join(machine, "user-env.json")
	if err := os.WriteFile(environment, data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(installbase.UserEnvFileVariable, environment)
	t.Setenv("LOCALAPPDATA", local)

	// Act
	plan, err := findPlan()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if !installbase.SameDirectory(plan.Folder, root) {
		t.Errorf("the plan installs into %s, want %s", plan.Folder, root)
	}
	if plan.Note != keptNote {
		t.Errorf("the plan says %q, want %q", plan.Note, keptNote)
	}
}
