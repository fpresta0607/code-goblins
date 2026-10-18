package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
)

// A Claude Code CFO starts with the settings file its start writes in the
// home's state folder, which gives Claude Code the CFO's 400K auto-compact
// window, so it compacts long before its model's 1M window fills.
func TestTheClaudeCFOsSettingsFileGivesItsAutoCompactWindow(t *testing.T) {
	// Arrange
	root := t.TempDir()
	h := home.Home{Root: root, State: filepath.Join(root, "state"), Data: filepath.Join(root, "data")}

	// Act
	args, err := cfoHookArguments(h, "claude")

	// Assert
	path := filepath.Join(h.State, cfoSettingsFile)
	if err != nil || !slices.Equal(args, []string{"--settings", path}) {
		t.Fatalf("cfoHookArguments = %q, %v; want --settings %s", args, err, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		AutoCompactWindow int `json:"autoCompactWindow"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("%s is not a settings document: %v\n%s", path, err, data)
	}
	if settings.AutoCompactWindow != 400_000 {
		t.Errorf("%s gives an auto-compact window of %d, want 400000", path, settings.AutoCompactWindow)
	}
}

// A Codex CFO compacts within the same window, by Codex's own limit at 90
// percent of it; a Claude Code CFO takes its window from its settings file,
// and pi has no setting for one.
func TestTheCodexCFOCompactsWithinTheCFOsWindow(t *testing.T) {
	for _, tc := range []struct {
		harness string
		want    []string
	}{
		{"codex", []string{"-c", "model_auto_compact_token_limit=360000"}},
		{"claude", nil},
		{"pi", nil},
	} {
		t.Run(tc.harness, func(t *testing.T) {
			// Act
			got := cfoCompactArguments(tc.harness)

			// Assert
			if !slices.Equal(got, tc.want) {
				t.Errorf("cfoCompactArguments(%s) = %q, want %q", tc.harness, got, tc.want)
			}
		})
	}
}
