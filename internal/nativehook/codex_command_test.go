package nativehook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// codexEvents are the events Install hooks for Codex.
var codexEvents = []string{"SessionStart", "UserPromptSubmit", "PostToolUse", "Stop", "SessionEnd", "SubagentStart", "SubagentStop", "Interrupt", "PreCompact", "PostCompact"}

// codexHandlers reads every hook handler in a Codex hooks.json by event.
func codexHandlers(t *testing.T, dir string) map[string][]struct {
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
} {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	handlers := map[string][]struct {
		Command string `json:"command"`
		Timeout int    `json:"timeout"`
	}{}
	for event, groups := range document.Hooks {
		for _, group := range groups {
			handlers[event] = append(handlers[event], group.Hooks...)
		}
	}
	return handlers
}

// rootPath is an absolute path at the root of dir's volume, so a test names
// paths that hold no character the temporary directory happens to carry.
func rootPath(dir string, elements ...string) string {
	return filepath.Join(append([]string{filepath.VolumeName(dir) + string(filepath.Separator)}, elements...)...)
}

// Codex runs each hook command inside the session's own shell, which on
// Windows is powershell.exe -NoProfile -Command, so a hook that started the
// helper's PowerShell started two for every event and timed out live. Its
// hooks run cfo itself, written so PowerShell, cmd and bash read it alike.
func TestCodexHooksRunCfoWithoutAHelperShell(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	config := InstallConfig{Harness: "codex", ConfigDir: dir, Executable: rootPath(dir, "cg", "bin", "cfo.exe"), Home: rootPath(dir, "cg"), State: rootPath(dir, "cg", "state")}
	want := filepath.ToSlash(config.Executable) + " native-hook codex --home " + filepath.ToSlash(config.Home) + " --state " + filepath.ToSlash(config.State)

	// Act
	if _, err := Install(config); err != nil {
		t.Fatal(err)
	}

	// Assert
	handlers := codexHandlers(t, dir)
	for _, event := range codexEvents {
		if len(handlers[event]) != 1 || handlers[event][0].Command != want || handlers[event][0].Timeout != TimeoutSeconds {
			t.Errorf("%s handlers = %+v, want only %q with the %d-second limit", event, handlers[event], want, TimeoutSeconds)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "cfo-native-hook.ps1")); !os.IsNotExist(err) {
		t.Errorf("a helper script was written for hooks that do not run it: %v", err)
	}
}

// A path that would need quoting reads differently in PowerShell, cmd and
// bash, so such an install keeps the helper and its one quoted path.
func TestCodexHooksKeepTheHelperWhereAPathNeedsQuoting(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	config := InstallConfig{Harness: "codex", ConfigDir: dir, Executable: rootPath(dir, "Code Goblins", "bin", "cfo.exe"), Home: rootPath(dir, "cg"), State: rootPath(dir, "cg", "state")}
	_, want := helperCommand(dir)

	// Act
	if _, err := Install(config); err != nil {
		t.Fatal(err)
	}

	// Assert
	handlers := codexHandlers(t, dir)
	for _, event := range codexEvents {
		if len(handlers[event]) != 1 || handlers[event][0].Command != want {
			t.Errorf("%s handlers = %+v, want only the helper's %q", event, handlers[event], want)
		}
	}
	script, err := os.ReadFile(filepath.Join(dir, "cfo-native-hook.ps1"))
	if err != nil || !strings.Contains(string(script), config.Executable) {
		t.Errorf("the helper does not run %s (%v): %s", config.Executable, err, script)
	}
}

// A reinstall replaces the hooks an earlier install wrote in either shape, or
// from a binary since moved, and leaves no helper the hooks no longer run.
func TestCodexReinstallReplacesEitherCommandShape(t *testing.T) {
	for _, test := range []struct {
		name, before, after string
	}{
		{"from the helper", "Code Goblins", "cg"},
		{"to the helper", "cg", "Code Goblins"},
		{"after the binary moved", "old-cg", "cg"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			dir := t.TempDir()
			config := func(folder string) InstallConfig {
				return InstallConfig{Harness: "codex", ConfigDir: dir, Executable: rootPath(dir, folder, "cfo.exe"), Home: rootPath(dir, "cg"), State: rootPath(dir, "cg", "state")}
			}
			if _, err := Install(config(test.before)); err != nil {
				t.Fatal(err)
			}

			// Act
			if _, err := Install(config(test.after)); err != nil {
				t.Fatal(err)
			}

			// Assert
			usesHelper := strings.Contains(test.after, " ")
			_, want := helperCommand(dir)
			if !usesHelper {
				after := config(test.after)
				want = filepath.ToSlash(after.Executable) + " native-hook codex --home " + filepath.ToSlash(after.Home) + " --state " + filepath.ToSlash(after.State)
			}
			handlers := codexHandlers(t, dir)
			for _, event := range codexEvents {
				if len(handlers[event]) != 1 || handlers[event][0].Command != want {
					t.Errorf("%s handlers = %+v, want only %q", event, handlers[event], want)
				}
			}
			if _, err := os.Stat(filepath.Join(dir, "cfo-native-hook.ps1")); usesHelper == os.IsNotExist(err) {
				t.Errorf("helper script present = %v, want %v", !os.IsNotExist(err), usesHelper)
			}
		})
	}
}

// Uninstall, which knows only the configuration folder, removes the hooks in
// either shape and leaves the unrelated one.
func TestUninstallRemovesEitherCodexCommandShape(t *testing.T) {
	for _, folder := range []string{"cg", "Code Goblins"} {
		t.Run(folder, func(t *testing.T) {
			// Arrange
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "hooks.json"), []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"C:/tools/cfo.exe native-hook claude --home C:/cg --state C:/cg/state"}]}]}}`), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Install(InstallConfig{Harness: "codex", ConfigDir: dir, Executable: rootPath(dir, folder, "cfo.exe"), Home: rootPath(dir, "cg"), State: rootPath(dir, "cg", "state")}); err != nil {
				t.Fatal(err)
			}

			// Act
			removed, err := Uninstall("codex", dir)

			// Assert
			if err != nil || !removed {
				t.Fatalf("Uninstall = %v, %v; want the board hooks removed", removed, err)
			}
			handlers := codexHandlers(t, dir)
			if len(handlers) != 1 || len(handlers["Stop"]) != 1 || !strings.Contains(handlers["Stop"][0].Command, "native-hook claude") {
				t.Errorf("handlers after uninstall = %+v, want only the unrelated Stop hook", handlers)
			}
		})
	}
}

// An install from a build that held the hooks to 3 seconds, or the pi
// extension to 2.5, gets the native hooks' limit on the next install, with
// one handler for each event as before.
func TestAnInstallWithTheOldLimitTakesTheNewOneOnTheNextInstall(t *testing.T) {
	for _, harness := range []string{"codex", "pi"} {
		t.Run(harness, func(t *testing.T) {
			// Arrange
			dir := t.TempDir()
			config := InstallConfig{Harness: harness, ConfigDir: dir, Executable: rootPath(dir, "cg", "bin", "cfo.exe"), Home: rootPath(dir, "cg"), State: rootPath(dir, "cg", "state")}
			path, err := Install(config)
			if err != nil {
				t.Fatal(err)
			}
			installed, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			// The limit is a handler's last member, so a line ends after it.
			current, older := `"timeout": `+strconv.Itoa(TimeoutSeconds)+"\n", `"timeout": 3`+"\n"
			if harness == "pi" {
				current, older = "timeout: "+strconv.Itoa(TimeoutSeconds*1000), "timeout: 2500"
			}
			if !strings.Contains(string(installed), current) {
				t.Fatalf("installed %s holds no %q:\n%s", path, current, installed)
			}
			if err := os.WriteFile(path, []byte(strings.ReplaceAll(string(installed), current, older)), 0o600); err != nil {
				t.Fatal(err)
			}

			// Act
			if _, err := Install(config); err != nil {
				t.Fatal(err)
			}

			// Assert
			reinstalled, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(reinstalled), older) || !strings.Contains(string(reinstalled), current) {
				t.Fatalf("reinstalled %s:\n%s\nwant %q in place of %q", path, reinstalled, current, older)
			}
			if harness == "codex" {
				for event, handlers := range codexHandlers(t, dir) {
					if len(handlers) != 1 || handlers[0].Timeout != TimeoutSeconds {
						t.Errorf("%s handlers = %+v, want one held to %d seconds", event, handlers, TimeoutSeconds)
					}
				}
			}
		})
	}
}
