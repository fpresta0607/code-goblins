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
		{name: "a weekly floor under 0", settings: `{"weekly_floor_percent":{"claude":-1}}`},
		{name: "a weekly floor of the whole week", settings: `{"weekly_floor_percent":{"claude":100}}`},
		{name: "a weekly floor for a provider with no measured week", settings: `{"weekly_floor_percent":{"pi":0}}`},
		{name: "a weekly floor for a misspelt provider", settings: `{"weekly_floor_percent":{"Claude":0}}`},
		{name: "a weekly floor that is no number", settings: `{"weekly_floor_percent":{"claude":"0"}}`},
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

// The Overlord, 2026-10-09: "keep using claude until at 0". A home that never
// set a provider's floor keeps 5 percent of its week back, and one provider's
// floor leaves every other provider at its own.
func TestWeeklyFloorIsEachProvidersOwnWithFivePercentAsTheDefault(t *testing.T) {
	for _, testCase := range []struct {
		name, settings        string
		wantClaude, wantCodex float64
	}{
		{name: "no file", wantClaude: 5, wantCodex: 5},
		{name: "a file without the setting", settings: `{"disk_floor_gb":20}`, wantClaude: 5, wantCodex: 5},
		{name: "claude at 0", settings: `{"weekly_floor_percent":{"claude":0}}`, wantClaude: 0, wantCodex: 5},
		{name: "codex at 2.5", settings: `{"weekly_floor_percent":{"codex":2.5}}`, wantClaude: 5, wantCodex: 2.5},
		{name: "both set", settings: `{"weekly_floor_percent":{"claude":0,"codex":10}}`, wantClaude: 0, wantCodex: 10},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			root := t.TempDir()
			if testCase.settings != "" {
				root = writeSettings(t, testCase.settings)
			}

			// Act
			settings, err := Read(root)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if claude, codex := settings.WeeklyFloor("claude"), settings.WeeklyFloor("codex"); claude != testCase.wantClaude || codex != testCase.wantCodex {
				t.Fatalf("floors claude=%v codex=%v, want claude=%v codex=%v", claude, codex, testCase.wantClaude, testCase.wantCodex)
			}
		})
	}
}

func TestSetWeeklyFloorKeepsEverySettingButTheOneItSets(t *testing.T) {
	for _, testCase := range []struct {
		name, settings string
		wantDiskFloor  float64
		wantOwners     string
		wantCodex      float64
	}{
		{name: "no file", wantDiskFloor: 15, wantCodex: 5},
		{name: "other settings and codex's floor", settings: `{"disk_floor_gb":20,"github_owners":["my-org"],"weekly_floor_percent":{"codex":3}}`, wantDiskFloor: 20, wantOwners: "my-org", wantCodex: 3},
		{name: "claude's floor set before", settings: `{"weekly_floor_percent":{"claude":7}}`, wantDiskFloor: 15, wantCodex: 5},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			root := t.TempDir()
			if testCase.settings != "" {
				root = writeSettings(t, testCase.settings)
			}

			// Act
			err := SetWeeklyFloor(root, "claude", 0)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			settings, err := Read(root)
			if err != nil {
				t.Fatal(err)
			}
			if settings.WeeklyFloor("claude") != 0 || settings.WeeklyFloor("codex") != testCase.wantCodex || settings.DiskFloorGB != testCase.wantDiskFloor || strings.Join(settings.GitHubOwners, ",") != testCase.wantOwners {
				t.Fatalf("after SetWeeklyFloor Read gave %+v", settings)
			}
		})
	}
}

func TestSetWeeklyFloorRefusesWhatReadWouldRefuseAndWritesNothing(t *testing.T) {
	for _, testCase := range []struct {
		name, provider string
		percent        float64
	}{
		{name: "under 0", provider: "claude", percent: -1},
		{name: "the whole week", provider: "claude", percent: 100},
		{name: "a provider with no measured week", provider: "pi", percent: 0},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			const before = `{"disk_floor_gb":20}`
			root := writeSettings(t, before)

			// Act
			err := SetWeeklyFloor(root, testCase.provider, testCase.percent)

			// Assert
			data, readErr := os.ReadFile(filepath.Join(root, "config", "fleet.json"))
			if err == nil || readErr != nil || string(data) != before {
				t.Fatalf("err=%v, file %s (%v); want a refusal and the file untouched", err, data, readErr)
			}
		})
	}
}

func TestSetWeeklyFloorLeavesAFileReadRefusesAsItWas(t *testing.T) {
	const before = `{"max_live_goblins":128}`
	root := writeSettings(t, before)

	err := SetWeeklyFloor(root, "claude", 0)

	data, readErr := os.ReadFile(filepath.Join(root, "config", "fleet.json"))
	if err == nil || readErr != nil || string(data) != before {
		t.Fatalf("err=%v, file %s (%v); want a refusal and the file untouched", err, data, readErr)
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
