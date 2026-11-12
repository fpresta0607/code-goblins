package install

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"
)

// The CEO compacted only at 882K tokens of its 1M window, under the
// Overlord's CLAUDE_AUTOCOMPACT_PCT_OVERRIDE of 90, and a restart came first
// in its conversation of 2026-10-18, and goblins carried a median 302K tokens
// into every request of the week to 2026-10-27, up to 882K. Both terminals'
// settings give Claude Code a 400K auto-compact window, so each compacts at
// about 342K.
func TestTheCEOsAndAGoblinsTerminalsCompactWithinA400KWindow(t *testing.T) {
	for _, tc := range []struct {
		name     string
		settings func(root, stateDir string) ([]byte, error)
		want     any
	}{
		{name: "the CEO's", settings: CEOSettings, want: json.Number("400000")},
		{name: "a goblin's", settings: func(root, stateDir string) ([]byte, error) {
			return GoblinSettings(root, stateDir, "http://127.0.0.1:4310")
		}, want: json.Number("400000")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			root := t.TempDir()

			// Act
			data, err := tc.settings(root, filepath.Join(root, "state"))

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			var document map[string]any
			decoder := json.NewDecoder(bytes.NewReader(data))
			decoder.UseNumber()
			if err := decoder.Decode(&document); err != nil {
				t.Fatalf("the settings are not a JSON object: %v\n%s", err, data)
			}
			if got := document["autoCompactWindow"]; got != tc.want {
				t.Errorf("autoCompactWindow = %v, want %v", got, tc.want)
			}
		})
	}
}

// The Overlord, 2026-10-28: "why is it so slow and not sending". Claude Code
// draws a suggested next prompt on the CEO's empty input line as each turn
// ends, which the board reads as words left unsent, so it typed none of his
// messages while the CEO sat idle. The CEO's terminal starts without them, as
// a goblin's does.
func TestTheCEOsTerminalStartsWithoutPromptSuggestions(t *testing.T) {
	// Arrange
	root := t.TempDir()

	// Act
	data, err := CEOSettings(root, filepath.Join(root, "state"))

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("the settings are not a JSON object: %v\n%s", err, data)
	}
	if got, isSet := document["promptSuggestionEnabled"]; !isSet || got != false {
		t.Errorf("promptSuggestionEnabled = %v (set %t), want false", got, isSet)
	}
}
