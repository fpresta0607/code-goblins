package install

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
)

// A goblin's tool call ran the native hook and the key alarm after it, and a
// shell call the seal before it too, each hook a cfo.exe process with a
// console host of its own. One process runs per hook event now: the seal
// before a shell call, and after every tool call the native hook, which
// raises the key alarm too, within the alarm's limit, the native hook
// keeping its own inside it.
func TestAGoblinStartsOneCfoPerHookEvent(t *testing.T) {
	// Arrange
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	settings, err := GoblinSettings(root, stateDir)
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
		"PreToolUse":  {Event: "PreToolUse", Matcher: "Bash|PowerShell|Monitor", Command: executable, Args: []string{"hook", "goblin-seal"}},
		"PostToolUse": {Event: "PostToolUse", Command: executable, Args: []string{"native-hook", "claude", "--home", root, "--state", stateDir, "--key-alarm"}, Timeout: KeyAlarmSeconds},
	}
	for _, event := range []string{"PreToolUse", "PostToolUse"} {
		entries := slices.DeleteFunc(slices.Clone(hooks), func(hook registeredHook) bool { return hook.Event != event })
		if expected := want[event]; len(entries) != 1 || entries[0].Matcher != expected.Matcher || entries[0].Command != expected.Command || !slices.Equal(entries[0].Args, expected.Args) || entries[0].Timeout != expected.Timeout || entries[0].Shell {
			t.Errorf("%s hooks = %+v, want only %+v run with no shell", event, entries, expected)
		}
	}
	processes := func(tool string) int {
		total := 0
		for _, hook := range hooks {
			if (hook.Event == "PreToolUse" || hook.Event == "PostToolUse") && selects(t, hook.Matcher, tool) {
				total++
			}
		}
		return total
	}
	for tool, wantProcesses := range map[string]int{"Bash": 2, "PowerShell": 2, "Read": 1, "Edit": 1, "Grep": 1, "WebFetch": 1} {
		if got := processes(tool); got != wantProcesses {
			t.Errorf("a %s call starts %d cfo.exe for the goblin's hooks, want %d", tool, got, wantProcesses)
		}
	}
}
