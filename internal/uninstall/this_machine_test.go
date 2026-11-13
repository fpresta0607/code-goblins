package uninstall

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/cutover/cutovertest"
	"github.com/fpresta0607/code-goblins/internal/installbase"
)

// A home outside the per-user folder that only the home setting of a build
// before the rename names, as v0.5.18 and earlier left one, is the home the
// uninstall finds, as the install does, and not the per-user folder (issue
// 2539).
func TestThisMachineFindsAHomeOnlyTheOldHomeSettingNames(t *testing.T) {
	for name, settings := range map[string]map[string]string{
		"the old setting alone":       {cutovertest.HomeSetting: "{home}"},
		"both settings, the new wins": {cutovertest.HomeSetting: "{other}", installbase.HomeVariable: "{home}"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			machine := t.TempDir()
			local := filepath.Join(machine, "Local")
			root := filepath.Join(machine, "CodeGoblins")
			other := filepath.Join(machine, "Other")
			for _, folder := range []string{filepath.Join(root, "state"), filepath.Join(other, "state"), local} {
				if err := os.MkdirAll(folder, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			values := map[string]string{}
			for setting, value := range settings {
				values[setting] = map[string]string{"{home}": root, "{other}": other}[value]
			}
			data, err := json.Marshal(values)
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
			found, err := ThisMachine()

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if !installbase.SameDirectory(found.Home.Root, root) {
				t.Errorf("the uninstall found the home %s, want %s", found.Home.Root, root)
			}
		})
	}
}
