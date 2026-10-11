package nativehook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func installFor(t *testing.T, harness, dir string) {
	t.Helper()
	if _, err := Install(InstallConfig{Harness: harness, ConfigDir: dir, Executable: filepath.Join(dir, "cfo.exe"), Home: dir, State: filepath.Join(dir, "state")}); err != nil {
		t.Fatal(err)
	}
}

// Uninstall takes out the board's hooks from every event and its helper, and
// leaves the settings and hooks that are not the board's exactly as they mean.
func TestUninstallRemovesOnlyTheBoardHooks(t *testing.T) {
	for _, name := range []string{"claude", "codex"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := settingsPath(name, dir)
			if err := os.WriteFile(path, []byte(`{"theme":"dark","hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo unrelated"}]}]}}`), 0600); err != nil {
				t.Fatal(err)
			}
			installFor(t, name, dir)

			removed, err := Uninstall(name, dir)

			if err != nil || !removed {
				t.Fatalf("Uninstall = %v, %v; want the board hooks removed", removed, err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var doc struct {
				Theme string                       `json:"theme"`
				Hooks map[string][]json.RawMessage `json:"hooks"`
			}
			if err := json.Unmarshal(data, &doc); err != nil {
				t.Fatal(err)
			}
			if doc.Theme != "dark" || len(doc.Hooks) != 1 || len(doc.Hooks["Stop"]) != 1 || !strings.Contains(string(doc.Hooks["Stop"][0]), "echo unrelated") {
				t.Fatalf("settings after uninstall = %s, want only the unrelated Stop hook and the theme", data)
			}
			if _, err := os.Stat(filepath.Join(dir, "cfo-native-hook.ps1")); !os.IsNotExist(err) {
				t.Errorf("the helper script survived the uninstall: %v", err)
			}
			if removed, err := Uninstall(name, dir); err != nil || removed {
				t.Errorf("a second Uninstall = %v, %v; want nothing left to remove", removed, err)
			}
		})
	}
}

// A file Install did not write keeps its place, even at the helper's path.
func TestUninstallLeavesFilesItDoesNotOwn(t *testing.T) {
	for _, test := range []struct {
		harness, file string
	}{
		{"claude", "cfo-native-hook.ps1"},
		{"pi", filepath.Join("extensions", "cfo-native.ts")},
	} {
		t.Run(test.harness, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, test.file)
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("someone else's script"), 0600); err != nil {
				t.Fatal(err)
			}

			removed, err := Uninstall(test.harness, dir)

			if err != nil || removed {
				t.Fatalf("Uninstall = %v, %v; want nothing removed", removed, err)
			}
			if data, _ := os.ReadFile(path); string(data) != "someone else's script" {
				t.Errorf("%s = %q, want it untouched", test.file, data)
			}
		})
	}
}

func TestUninstallRemovesThePiExtension(t *testing.T) {
	dir := t.TempDir()
	installFor(t, "pi", dir)

	removed, err := Uninstall("pi", dir)

	if err != nil || !removed {
		t.Fatalf("Uninstall = %v, %v; want the extension removed", removed, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "extensions", "cfo-native.ts")); !os.IsNotExist(err) {
		t.Errorf("the Pi extension survived the uninstall: %v", err)
	}
}

// Broken settings are refused, not rewritten.
func TestUninstallRefusesBrokenSettings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hooks.json")
	if err := os.WriteFile(path, []byte(`{"hooks":42}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Uninstall("codex", dir); err == nil {
		t.Fatal("Uninstall accepted a malformed hooks object")
	}
	if data, _ := os.ReadFile(path); string(data) != `{"hooks":42}` {
		t.Fatalf("Uninstall rewrote broken settings: %s", data)
	}
}

// Installed reads whether the board's hooks are there without changing
// anything, so an uninstall can read its removal back: not before an install,
// there after it, and gone after an uninstall, for every harness, and a file
// of the person's own at the helper's path is not the board's.
func TestInstalledSaysWhetherTheBoardHooksAreThere(t *testing.T) {
	for _, name := range []string{"claude", "codex", "pi"} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			dir := t.TempDir()
			before, beforeErr := Installed(name, dir)
			installFor(t, name, dir)

			// Act
			during, duringErr := Installed(name, dir)
			if _, err := Uninstall(name, dir); err != nil {
				t.Fatal(err)
			}
			after, afterErr := Installed(name, dir)

			// Assert
			if before || beforeErr != nil || !during || duringErr != nil || after || afterErr != nil {
				t.Errorf("Installed before, during and after = %v (%v), %v (%v), %v (%v); want false, true, false", before, beforeErr, during, duringErr, after, afterErr)
			}
		})
	}
	t.Run("a file of the person's at the helper's path", func(t *testing.T) {
		// Arrange
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "cfo-native-hook.ps1"), []byte("# mine\n"), 0o600); err != nil {
			t.Fatal(err)
		}

		// Act
		there, err := Installed("claude", dir)

		// Assert
		if there || err != nil {
			t.Errorf("Installed = %v, %v; want a file the board did not write not counted", there, err)
		}
	})
}
