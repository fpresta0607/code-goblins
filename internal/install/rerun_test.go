package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/nativehook"
)

// appendAdopterHook adds a hook of the adopter's own to the end of event's
// list, as an adopter editing settings.json by hand after an install would.
func appendAdopterHook(t *testing.T, path, event, command string) {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal([]byte(readFile(t, path)), &document); err != nil {
		t.Fatal(err)
	}
	events := document["hooks"].(map[string]any)
	groups, _ := events[event].([]any)
	events[event] = append(groups, map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command}}})
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// mergeCFOHooks adds the CFO's hooks to the settings file at path, each group
// after the ones its event holds, as cfo install merged them into the user's
// settings until 2026-10.
func mergeCFOHooks(t *testing.T, path, root string) {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal([]byte(readFile(t, path)), &document); err != nil {
		t.Fatal(err)
	}
	events, _ := document["hooks"].(map[string]any)
	if events == nil {
		events = map[string]any{}
		document["hooks"] = events
	}
	for _, group := range cfoHookGroups(root) {
		rendered := map[string]any{"hooks": group.entries}
		if group.matcher != "" {
			rendered["matcher"] = group.matcher
		}
		groups, _ := events[group.event].([]any)
		events[group.event] = append(groups, rendered)
	}
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// cfoCommands is every command line in the settings file at path that runs a
// CFO hook.
func cfoCommands(t *testing.T, path string) []string {
	t.Helper()
	var commands []string
	for _, command := range hookCommands(t, path) {
		if isCFOCommand(command) {
			commands = append(commands, command)
		}
	}
	return commands
}

// Until 2026-10 cfo install merged the CFO's hooks into the user's settings,
// where every Claude Code session of the user ran them: each Bash call, turn
// end and start, in his own sessions and in every goblin's, started cfo.exe,
// about 0.1 s each, for a hook that left on its environment. The CFO's
// terminal starts with its hooks, so an install writes none there.
func TestInstallWritesNoCFOHookIntoTheUsersSettings(t *testing.T) {
	// Arrange
	f := newFixture(t, adopterSettings, nil)

	// Act
	f.install()

	// Assert
	if commands := cfoCommands(t, f.user); len(commands) != 0 {
		t.Errorf("the install wrote %d CFO hook(s) into the user's settings, which every session of the user runs:\n%s", len(commands), strings.Join(commands, "\n"))
	}
}

// A machine a build before 2026-10 installed holds the CFO's hooks in the
// user's settings. An install takes them out, keeps every hook that is not
// the CFO's, and a second install changes nothing.
func TestInstallTakesTheCFOsHooksOutOfTheUsersSettings(t *testing.T) {
	// Arrange
	f := newFixture(t, adopterSettings, nil)
	mergeCFOHooks(t, f.user, f.root)
	if held := cfoCommands(t, f.user); len(held) != len(Hooks(f.root)) {
		t.Fatalf("the settings hold %d CFO hook(s) before the install, want the %d an older build wrote", len(held), len(Hooks(f.root)))
	}

	// Act
	output := f.install()

	// Assert
	if commands := cfoCommands(t, f.user); len(commands) != 0 {
		t.Errorf("the install left %d CFO hook(s) in the user's settings:\n%s", len(commands), strings.Join(commands, "\n"))
	}
	commands := hookCommands(t, f.user)
	for _, foreign := range foreignCommands {
		if count(commands, foreign) != 1 {
			t.Errorf("the adopter's hook %q appears %d times, want 1", foreign, count(commands, foreign))
		}
	}
	if !strings.Contains(output, "removed the CFO hooks") {
		t.Errorf("the output does not say the hooks were taken out:\n%s", output)
	}
	before := readFile(t, f.user)
	f.install()
	if after := readFile(t, f.user); after != before {
		t.Errorf("a second install rewrote the settings\nbefore: %s\nafter: %s", before, after)
	}
}

// cfo install and cfo hooks install claude write the same settings.json. Each
// puts its own hooks back where they stand, so once both have run, running
// either again rewrites nothing. Each used to move its entries to the end of
// the shared lists, behind the other's, so every rerun of either rewrote the
// file. The two render JSON differently, one escaping & < > and the other
// not, and that difference alone moves nothing either.
func TestReinstallingBothHookWritersRewritesNothing(t *testing.T) {
	for name, settings := range map[string]string{
		"adopter settings":               adopterSettings,
		"a command with shell operators": strings.Replace(adopterSettings, `"node session-end.js"`, `"node session-end.js && echo done 2>&1"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, settings, nil)
			claude := filepath.Dir(f.user)
			native := nativehook.InstallConfig{Harness: "claude", ConfigDir: claude, Executable: filepath.Join(claude, "cfo.exe"), Home: f.root, State: filepath.Join(f.root, "state")}
			f.install()
			if _, err := nativehook.Install(native); err != nil {
				t.Fatal(err)
			}
			settled := readFile(t, f.user)

			f.install()
			afterInstall := readFile(t, f.user)
			if _, err := nativehook.Install(native); err != nil {
				t.Fatal(err)
			}
			afterNative := readFile(t, f.user)

			if afterInstall != settled {
				t.Errorf("rerunning cfo install rewrote the shared settings\nbefore: %s\nafter: %s", settled, afterInstall)
			}
			if afterNative != settled {
				t.Errorf("rerunning cfo hooks install claude rewrote the shared settings\nbefore: %s\nafter: %s", settled, afterNative)
			}
		})
	}
}
