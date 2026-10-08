package fleetconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func writeSettings(t *testing.T, content string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config", "fleet.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestReadRefusesAFileThatDoesNotMeanWhatItSays(t *testing.T) {
	for _, testCase := range []struct{ name, settings string }{
		{name: "null", settings: `null`},
		{name: "unknown key", settings: `{"maximum":4}`},
		{name: "two objects", settings: `{} {}`},
		// Goblin slots go by memory alone; a count of live goblins is no
		// setting, so a file that still sets one is refused, not obeyed.
		{name: "a live goblin count", settings: `{"max_live_goblins":128}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			root := writeSettings(t, testCase.settings)

			// Act
			_, err := Read(root)

			// Assert
			if err == nil {
				t.Fatalf("%s was read as settings", testCase.settings)
			}
		})
	}
}

func TestReadWithoutAFileIsTheDefaults(t *testing.T) {
	settings, err := Read(t.TempDir())

	if err != nil || settings.DiskFloorGB != Defaults().DiskFloorGB {
		t.Fatalf("settings=%+v err=%v, want the defaults", settings, err)
	}
}
