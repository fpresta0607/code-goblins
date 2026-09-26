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

// A rerun of cfo install leaves the CFO hooks where they stand, even behind a
// hook the adopter added after them, so it changes nothing. It used to move
// them to the end of the list, behind the adopter's, and rewrite the file.
func TestReinstallKeepsTheCFOHooksWhereTheyStand(t *testing.T) {
	f := newFixture(t, adopterSettings, nil)
	f.install()
	appendAdopterHook(t, f.user, cfoHookGroups()[0].event, "node added-later.js")
	before := readFile(t, f.user)

	output := f.install()

	if after := readFile(t, f.user); after != before {
		t.Errorf("a rerun rewrote the settings\nbefore: %s\nafter: %s", before, after)
	}
	if !strings.Contains(output, "user hooks") || !strings.Contains(output, "already in") {
		t.Errorf("output does not report the hooks already in place:\n%s", output)
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
