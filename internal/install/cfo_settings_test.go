package install

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"
)

// The CFO compacted only at 882K tokens of its 1M window, under the
// Overlord's CLAUDE_AUTOCOMPACT_PCT_OVERRIDE of 90, and a restart came first
// in its conversation of 2026-10-18. Its terminal's settings give Claude Code
// a 400K auto-compact window, so it compacts at about 342K; a goblin's keep
// Claude Code's own window.
func TestTheCFOsTerminalCompactsWithinA400KWindowAndAGoblinsWithinClaudeCodes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		settings func(root, stateDir string) ([]byte, error)
		want     any
	}{
		{name: "the CFO's", settings: CFOSettings, want: json.Number("400000")},
		{name: "a goblin's", settings: GoblinSettings, want: nil},
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
