package fleetconfig

import (
	"os"
	"path/filepath"
	"strings"
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

// The live home held {"max_live_goblins":128} when goblin slots went to
// memory alone, and Read refuses a key it does not know, so an upgrade that
// left it there would refuse every start. RetireKeys takes it out and keeps
// the rest; a file it cannot read as one object is left for Read to report.
func TestRetireKeysTakesOutOnlyRetiredSettings(t *testing.T) {
	for _, testCase := range []struct {
		name, settings string
		isTaken        bool
		wantFloor      float64
	}{
		{name: "the count beside another setting", settings: `{"max_live_goblins":128,"disk_floor_gb":20}`, isTaken: true, wantFloor: 20},
		{name: "the count alone", settings: `{"max_live_goblins":128}`, isTaken: true, wantFloor: 15},
		{name: "no retired key", settings: `{"disk_floor_gb":20}`, wantFloor: 20},
		{name: "not one object", settings: `{} {}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			root := writeSettings(t, testCase.settings)
			path := filepath.Join(root, "config", "fleet.json")

			// Act
			said, err := RetireKeys(root)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !testCase.isTaken {
				if said != "" || string(data) != testCase.settings {
					t.Fatalf("said %q and left %s, want the file untouched", said, data)
				}
				return
			}
			if !strings.Contains(said, "max_live_goblins") || !strings.Contains(said, "memory") || strings.Contains(string(data), "max_live_goblins") {
				t.Fatalf("said %q and left %s, want the count taken out and named", said, data)
			}
			settings, err := Read(root)
			if err != nil || settings.DiskFloorGB != testCase.wantFloor {
				t.Fatalf("after RetireKeys Read gave %+v, %v; want disk_floor_gb %v", settings, err, testCase.wantFloor)
			}
		})
	}
}

func TestRetireKeysWithoutAFileWritesNone(t *testing.T) {
	root := t.TempDir()

	said, err := RetireKeys(root)

	if _, statErr := os.Stat(filepath.Join(root, "config", "fleet.json")); said != "" || err != nil || !os.IsNotExist(statErr) {
		t.Fatalf("said %q, err %v, file %v; want nothing said and no file", said, err, statErr)
	}
}
