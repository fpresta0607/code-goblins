package install

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
)

// claudeEvents are the Claude Code events a fleet session reports its
// session through.
var claudeEvents = []string{"SessionStart", "UserPromptSubmit", "PostToolUse", "Stop", "SessionEnd", "SubagentStart", "SubagentStop"}

// isNativeHook reports whether hook runs cfo native-hook, which reports its
// session to the supervisor, rather than a cfo hook.
func isNativeHook(hook registeredHook) bool {
	return len(hook.Args) > 0 && hook.Args[0] == "native-hook"
}

// Every Claude Code session the fleet starts, a goblin's and the CFO's,
// carries its native session hooks in its own launch settings, so it reports
// its session as a Codex or Pi session does, and nothing is written to the
// user's settings, which every Claude Code session on the machine loads.
func TestEveryFleetClaudeSessionCarriesItsNativeSessionHooks(t *testing.T) {
	for _, test := range []struct {
		name     string
		settings func(root, stateDir string) ([]byte, error)
	}{
		{"a goblin", GoblinSettings},
		{"the CFO", CFOSettings},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			root := t.TempDir()
			stateDir := filepath.Join(root, "state")
			settings, err := test.settings(root, stateDir)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "settings.json")
			if err := os.WriteFile(path, settings, 0o600); err != nil {
				t.Fatal(err)
			}

			// Act
			hooks := slices.DeleteFunc(registeredHooks(t, path), func(hook registeredHook) bool { return !isNativeHook(hook) })

			// Assert
			want := []string{"native-hook", "claude", "--home", root, "--state", stateDir}
			var events []string
			for _, hook := range hooks {
				if hook.Command != filepath.Join(root, home.BinDir, "cfo.exe") || !slices.Equal(hook.Args, want) || hook.Shell || hook.Matcher != "" {
					t.Errorf("native hook = %+v, want the home's cfo.exe run with %q and no shell for every tool", hook, want)
				}
				events = append(events, hook.Event)
			}
			slices.Sort(events)
			wantEvents := slices.Sorted(slices.Values(claudeEvents))
			if !slices.Equal(events, wantEvents) {
				t.Fatalf("native hook events = %q, want each of %q once", events, wantEvents)
			}
		})
	}
}
