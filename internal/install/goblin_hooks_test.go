package install

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
)

func TestGoblinSettingsHoldTheSealBeforeShellCallsAndTheAlarmAfterEveryTool(t *testing.T) {
	// Arrange
	root := t.TempDir()
	settings, err := GoblinSettings(root)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "goblin-claude-settings.json")
	if err := os.WriteFile(path, settings, 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	hooks := registeredHooks(t, path)

	// Assert
	executable := filepath.Join(root, home.BinDir, "cfo.exe")
	want := map[string]registeredHook{
		"goblin-seal":      {Event: "PreToolUse", Matcher: "Bash|PowerShell", Command: executable, Args: []string{"hook", "goblin-seal"}},
		"goblin-key-alarm": {Event: "PostToolUse", Matcher: "*", Command: executable, Args: []string{"hook", "goblin-key-alarm"}},
	}
	if len(hooks) != len(want) {
		t.Fatalf("hooks = %+v, want only the seal and the alarm", hooks)
	}
	for _, hook := range hooks {
		if expected := want[hook.Args[len(hook.Args)-1]]; hook.Event != expected.Event || hook.Matcher != expected.Matcher || hook.Command != expected.Command || !slices.Equal(hook.Args, expected.Args) || hook.Shell {
			t.Errorf("hook = %+v, want %+v run with no shell", hook, expected)
		}
	}
	for _, tool := range []string{"Bash", "PowerShell"} {
		if !selects(t, "Bash|PowerShell", tool) {
			t.Errorf("the seal does not run before %s", tool)
		}
	}
	for _, tool := range []string{"Read", "Edit", "Grep", "WebFetch"} {
		if selects(t, "Bash|PowerShell", tool) {
			t.Errorf("the seal runs before %s, which drives no browser", tool)
		}
	}
}
