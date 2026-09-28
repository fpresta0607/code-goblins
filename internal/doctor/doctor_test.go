package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/install"
)

// fakeClaudeVersion is what fakeClaude answers to --version.
const fakeClaudeVersion = "2.1.0 (Claude Code)"

// fakeClaude copies this test binary into dir as claude.exe, a program as the
// native build of Claude Code is, which TestMain answers as that build.
func fakeClaude(t *testing.T, dir string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	program, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "claude.exe"), program, 0o755); err != nil {
		t.Fatal(err)
	}
}

// fakeTool writes a .bat file that prints out and exits with code.
func fakeTool(t *testing.T, dir, name, out string, code int) {
	t.Helper()
	script := "@echo off\r\necho " + out + "\r\nexit /b " + strconv.Itoa(code) + "\r\n"
	if err := os.WriteFile(filepath.Join(dir, name+".bat"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestRunAllToolsPresent(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"git", "gh", "herdr", "tasks-axi", "quota-axi", "no-mistakes", "gh-axi", "chrome-devtools-axi"} {
		fakeTool(t, dir, name, name+" version 1.0.0", 0)
	}
	fakeTool(t, dir, "lavish-axi", "0.1.79-codegoblins.1", 0)
	fakeTool(t, dir, "winget", "v1.9.25200", 0)
	t.Setenv("PATH", dir)
	t.Setenv("CFO_HOME", t.TempDir()) // no .claude/settings.json: hook-pairing passes
	checks := Run()
	if len(checks) != 12 {
		t.Fatalf("len = %d, want 12 (10 tools + conpty + hook-pairing)", len(checks))
	}
	if !Healthy(checks) {
		t.Errorf("Healthy = false with all tools present: %+v", checks)
	}
	if checks[0].Name != "git" || checks[0].Version != "git version 1.0.0" {
		t.Errorf("git check = %+v, want captured version line", checks[0])
	}
	if checks[3].Name != "tasks-axi" || checks[4].Name != "quota-axi" {
		t.Errorf("AXI checks = %+v, want tasks-axi and quota-axi", checks[3:5])
	}
	if checks[5].Name != "no-mistakes" || checks[6].Name != "gh-axi" || checks[7].Name != "chrome-devtools-axi" {
		t.Errorf("gate/API checks = %+v, want no-mistakes, gh-axi, chrome-devtools-axi", checks[5:8])
	}
	if checks[8].Name != "lavish-axi" || checks[8].Err != "" || checks[8].Floor != "0.1.79" {
		t.Errorf("checks[8] = %+v, want the Code Goblins build of lavish-axi at its 0.1.79 floor", checks[8])
	}
	if checks[9].Name != "winget" || checks[9].Err != "" || !checks[9].Installer {
		t.Errorf("checks[9] = %+v, want winget as an installer-only check", checks[9])
	}
	if checks[10].Name != "conpty" || checks[10].Err != "" {
		t.Errorf("checks[10] = %+v, want this Windows's pseudo console available", checks[10])
	}
	if checks[11].Name != "hook-pairing" {
		t.Errorf("checks[11] = %+v, want hook-pairing", checks[11])
	}
}

// Without winget the check carries the one-line fix, and the environment is
// still healthy: only install.ps1 uses winget.
func TestRunMissingWingetIsInstallerOnly(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"git", "gh", "herdr", "tasks-axi", "quota-axi", "no-mistakes", "gh-axi", "chrome-devtools-axi"} {
		fakeTool(t, dir, name, name+" ok", 0)
	}
	fakeTool(t, dir, "lavish-axi", "0.1.79-codegoblins.1", 0)
	t.Setenv("PATH", dir)
	t.Setenv("CFO_HOME", t.TempDir())

	checks := Run()

	var winget Check
	for _, check := range checks {
		if check.Name == "winget" {
			winget = check
		}
	}
	if !winget.Installer || winget.Err != "not found on PATH" || !strings.Contains(winget.Hint, "https://apps.microsoft.com/detail/9NBLGGH4NNS1") {
		t.Errorf("winget check = %+v, want an installer-only check missing from PATH with the App Installer link", winget)
	}
	if !Healthy(checks) {
		t.Errorf("Healthy = false; a missing winget must never make doctor unhealthy: %+v", checks)
	}
}

// lavish-axi is pinned to the Code Goblins build from the fork: the upstream
// package, however new, reads as unavailable with the fork's install command,
// and like any presentation tool it never makes doctor unhealthy.
func TestRunLavishBelowFloorOrMissingIsPresentationOnly(t *testing.T) {
	for _, tc := range []struct {
		name    string
		version string
		wantErr bool
	}{
		{name: "missing", wantErr: true},
		{name: "below floor", version: "0.1.78-codegoblins.1", wantErr: true},
		{name: "older minor", version: "0.0.99", wantErr: true},
		{name: "unparseable", version: "lavish dev", wantErr: true},
		{name: "upstream at floor", version: "0.1.79", wantErr: true},
		{name: "upstream above floor", version: "lavish-axi v0.2.0", wantErr: true},
		{name: "the fork's build at floor", version: "0.1.79-codegoblins.1"},
		{name: "the fork's build above floor", version: "lavish-axi v0.2.0-codegoblins.3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, name := range []string{"git", "gh", "herdr", "tasks-axi", "quota-axi", "no-mistakes", "gh-axi", "chrome-devtools-axi"} {
				fakeTool(t, dir, name, name+" ok", 0)
			}
			if tc.version != "" {
				fakeTool(t, dir, "lavish-axi", tc.version, 0)
			}
			t.Setenv("PATH", dir)
			t.Setenv("CFO_HOME", t.TempDir())

			checks := Run()
			var lavish Check
			for _, check := range checks {
				if check.Name == "lavish-axi" {
					lavish = check
				}
			}
			if !lavish.Presentation || lavish.Hint != "npm install -g "+LavishRelease {
				t.Errorf("lavish-axi check = %+v, want a presentation check with its install command", lavish)
			}
			if (lavish.Err != "") != tc.wantErr {
				t.Errorf("lavish-axi Err = %q, want error %v", lavish.Err, tc.wantErr)
			}
			if !Healthy(checks) {
				t.Errorf("Healthy = false; lavish-axi must never make doctor unhealthy: %+v", checks)
			}
		})
	}
}

func TestMeetsFloorTreatsAMissingComponentAsZero(t *testing.T) {
	for _, tc := range []struct {
		versionLine string
		floor       string
		want        bool
	}{
		{versionLine: "1", floor: "1.0.1"},
		{versionLine: "1.0", floor: "1.0.1"},
		{versionLine: "1", floor: "1.0.0", want: true},
		{versionLine: "1.1", floor: "1.0.1", want: true},
		{versionLine: "2", floor: "1.0.1", want: true},
		{versionLine: "lavish-axi v0.1.71", floor: "0.1.71", want: true},
		{versionLine: "0.1.70", floor: "0.1.71"},
		{versionLine: "0.1.71.1", floor: "0.1.71", want: true},
		{versionLine: "0.1.79-codegoblins.1", floor: "0.1.79", want: true},
		{versionLine: "lavish-axi v0.1.78-codegoblins.9", floor: "0.1.79"},
		{versionLine: "dev", floor: "0.1.71"},
		{versionLine: "", floor: "0.1.71"},
	} {
		t.Run(tc.versionLine+" vs "+tc.floor, func(t *testing.T) {
			if got := meetsFloor(tc.versionLine, tc.floor); got != tc.want {
				t.Errorf("meetsFloor(%q, %q) = %v, want %v", tc.versionLine, tc.floor, got, tc.want)
			}
		})
	}
}

func TestRunMissingToolCarriesHint(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"git", "gh", "herdr"} {
		fakeTool(t, dir, name, name+" ok", 0)
	}
	t.Setenv("PATH", dir) // no tasks-axi
	t.Setenv("CFO_HOME", t.TempDir())
	checks := Run()
	if Healthy(checks) {
		t.Error("Healthy = true with tasks-axi missing")
	}
	last := checks[3]
	if last.Name != "tasks-axi" || last.Err == "" || last.Hint == "" {
		t.Errorf("tasks-axi check = %+v, want Err and Hint set", last)
	}
}

func TestRunBrokenToolReportsFailure(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"gh", "claude", "herdr"} {
		fakeTool(t, dir, name, name+" ok", 0)
	}
	fakeTool(t, dir, "git", "boom", 1)
	t.Setenv("PATH", dir)
	t.Setenv("CFO_HOME", t.TempDir())
	checks := Run()
	if Healthy(checks) {
		t.Error("Healthy = true with git --version failing")
	}
	if checks[0].Name != "git" || checks[0].Err == "" {
		t.Errorf("git check = %+v, want Err set", checks[0])
	}
}

// Herdr is reported but optional: a goblin or CFO in a native terminal needs
// none, so a missing or broken Herdr never makes the environment unhealthy.
func TestRunMissingOrBrokenHerdrIsOptional(t *testing.T) {
	for name, herdrExits := range map[string]int{"missing": -1, "broken": 1} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			for _, tool := range []string{"git", "gh", "tasks-axi", "quota-axi", "no-mistakes", "gh-axi", "chrome-devtools-axi"} {
				fakeTool(t, dir, tool, tool+" ok", 0)
			}
			fakeTool(t, dir, "lavish-axi", "0.1.79-codegoblins.1", 0)
			if herdrExits >= 0 {
				fakeTool(t, dir, "herdr", "boom", herdrExits)
			}
			t.Setenv("PATH", dir)
			t.Setenv("CFO_HOME", t.TempDir())

			checks := Run()

			if checks[2].Name != "herdr" || checks[2].Err == "" || !strings.Contains(checks[2].Optional, "Herdr") {
				t.Errorf("herdr check = %+v, want its failure reported as optional", checks[2])
			}
			if !Healthy(checks) {
				t.Errorf("Healthy = false with only herdr failing: %+v", checks)
			}
		})
	}
}

// Without a pseudo console no native terminal can run, so its absence makes
// the environment unhealthy with what Windows it needs.
func TestRunWithoutAPseudoConsoleIsUnhealthy(t *testing.T) {
	original := pseudoConsole
	pseudoConsole = windows.NewLazySystemDLL("kernel32.dll").NewProc("NoSuchPseudoConsoleProcedure")
	t.Cleanup(func() { pseudoConsole = original })

	check := checkConPTY()

	if check.Name != "conpty" || check.Err == "" || !strings.Contains(check.Hint, "1809") {
		t.Errorf("conpty check = %+v, want it missing with the Windows it needs", check)
	}
	if Healthy([]Check{check}) {
		t.Error("Healthy = true without a pseudo console")
	}
}

// A native terminal starts Claude Code as a program, so a claude found only
// as a script shim, such as npm's claude.cmd, is broken with the native
// build's installer.
func TestProbeHarnessesRefusesAClaudeScriptShim(t *testing.T) {
	dir := t.TempDir()
	fakeTool(t, dir, "claude", "claude 1.0.0", 0)
	t.Setenv("PATH", dir)

	probes := ProbeHarnesses(context.Background())

	if probes[0].Name != "claude" || probes[0].OK || !strings.Contains(probes[0].Detail, "claude.bat") || !strings.Contains(probes[0].Detail, "irm https://claude.ai/install.ps1 | iex") {
		t.Errorf("claude probe = %+v, want the script refused with the native build's installer", probes[0])
	}
}

func TestProbeHarnessesReportsMissingWithInstallHints(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"git", "gh", "herdr"} {
		fakeTool(t, dir, name, name+" ok", 0)
	}
	t.Setenv("PATH", dir)

	probes := ProbeHarnesses(context.Background())
	if len(probes) != 4 {
		t.Fatalf("len = %d, want 4 (every supported harness)", len(probes))
	}
	byName := make(map[string]HarnessProbe, len(probes))
	for _, probe := range probes {
		byName[probe.Name] = probe
	}
	for _, name := range []string{"claude", "codex", "pi", "kimi"} {
		probe, ok := byName[name]
		if !ok || probe.OK || !strings.Contains(probe.Detail, "not found on PATH") || !strings.Contains(probe.Detail, "install") {
			t.Errorf("%s probe = %+v, want missing harness with install hint", name, probe)
		}
	}
}

func TestRunMissingAXIToolsCarryInstallHints(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"git", "gh", "claude", "herdr", "codex", "pi"} {
		fakeTool(t, dir, name, name+" ok", 0)
	}
	t.Setenv("PATH", dir)
	t.Setenv("CFO_HOME", t.TempDir())

	checks := Run()
	byName := make(map[string]Check, len(checks))
	for _, check := range checks {
		byName[check.Name] = check
	}
	for _, name := range []string{"tasks-axi", "quota-axi"} {
		check, ok := byName[name]
		if !ok || check.Err == "" || check.Hint == "" {
			t.Errorf("%s check = %+v, want missing-tool error with install hint", name, check)
		}
	}
}

func TestRunMissingGateAndAXICapabilityToolsCarryInstallHints(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"git", "gh", "claude", "herdr", "codex", "pi", "kimi", "tasks-axi", "quota-axi"} {
		fakeTool(t, dir, name, name+" ok", 0)
	}
	t.Setenv("PATH", dir)
	t.Setenv("CFO_HOME", t.TempDir())

	checks := Run()
	byName := make(map[string]Check, len(checks))
	for _, check := range checks {
		byName[check.Name] = check
	}
	for _, name := range []string{"no-mistakes", "gh-axi", "chrome-devtools-axi"} {
		check, ok := byName[name]
		if !ok || check.Err == "" || check.Hint == "" {
			t.Errorf("%s check = %+v, want missing-tool error with install hint", name, check)
		}
	}
}

func TestProbeHarnessesReportsOkAndBroken(t *testing.T) {
	dir := t.TempDir()
	fakeClaude(t, dir)
	fakeTool(t, dir, "codex", "boom", 1)
	t.Setenv("PATH", dir)

	probes := ProbeHarnesses(context.Background())
	if len(probes) != 4 {
		t.Fatalf("len = %d, want 4 (every supported harness): %+v", len(probes), probes)
	}
	if probes[0].Name != "claude" || !probes[0].OK || probes[0].Detail != fakeClaudeVersion {
		t.Errorf("claude probe = %+v, want ok with the version line", probes[0])
	}
	if probes[1].Name != "codex" || probes[1].OK || probes[1].Detail == "" {
		t.Errorf("codex probe = %+v, want broken with failure detail", probes[1])
	}
	for _, index := range []int{2, 3} {
		if probes[index].OK || !strings.Contains(probes[index].Detail, "not found on PATH") {
			t.Errorf("probes[%d] = %+v, want missing harness reported broken", index, probes[index])
		}
	}
}

// writeSettings drops a .claude/settings.json under home with content.
func writeSettings(t *testing.T, home, content string) {
	t.Helper()
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestHookPairingBothPresentIsHealthy(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"cfo hook stop-autoarm"}]}],"PreToolUse":[{"hooks":[{"type":"command","command":"cfo hook turnend-guard"}]}]}}`)
	t.Setenv("CFO_HOME", dir)
	c := checkHookPairing()
	if c.Err != "" {
		t.Errorf("Err = %q, want empty with both hooks registered", c.Err)
	}
}

func TestHookPairingGuardWithoutAutoarmIsUnhealthy(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"cfo hook turnend-guard"}]}]}}`)
	t.Setenv("CFO_HOME", dir)
	c := checkHookPairing()
	if c.Err == "" {
		t.Fatal("Err = empty, want non-empty with the guard registered alone")
	}
	if c.Hint != hookPairingHint {
		t.Errorf("Hint = %q, want %q", c.Hint, hookPairingHint)
	}
}

func TestHookPairingNeitherPresentIsHealthy(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{"hooks":{}}`)
	t.Setenv("CFO_HOME", dir)
	c := checkHookPairing()
	if c.Err != "" {
		t.Errorf("Err = %q, want empty with neither hook registered", c.Err)
	}
}

func TestHookPairingFileAbsentIsHealthy(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CFO_HOME", dir)
	c := checkHookPairing()
	if c.Err != "" {
		t.Errorf("Err = %q, want empty with no settings.json at all", c.Err)
	}
}

func TestHookPairingMalformedJSONIsHealthy(t *testing.T) {
	dir := t.TempDir()
	// Malformed JSON that still contains the guard substring in plain text,
	// proving the check parses before searching rather than doing a raw
	// text scan that could false-positive on broken config.
	writeSettings(t, dir, `{"hooks": cfo hook turnend-guard NOT VALID JSON`)
	t.Setenv("CFO_HOME", dir)
	c := checkHookPairing()
	if c.Err != "" {
		t.Errorf("Err = %q, want empty (never a hard failure) with malformed JSON", c.Err)
	}
}

// TestHookPairingReadsTheUserScopeSettings covers the shape `cfo install`
// leaves behind: the CFO hooks live in the user settings and the checkout
// carries none, so a check that only read the checkout would go quiet
// exactly when the hooks were working.
func TestHookPairingReadsTheUserScopeSettings(t *testing.T) {
	t.Setenv("CFO_HOME", t.TempDir())
	configDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	settings := filepath.Join(configDir, "settings.json")

	if err := os.WriteFile(settings, []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"cfo hook turnend-guard"}]}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if c := checkHookPairing(); c.Err == "" {
		t.Error("Err = empty, want non-empty with the guard registered alone in the user settings")
	}

	if err := os.WriteFile(settings, []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"cfo hook turnend-guard"},{"type":"command","command":"cfo hook stop-autoarm"}]}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if c := checkHookPairing(); c.Err != "" {
		t.Errorf("Err = %q, want empty with both hooks registered in the user settings", c.Err)
	}
}

// TestHookPairingSpansBothScopes proves the two files are read together: a
// half-installed machine with the guard in one scope and the auto-arm in the
// other is healthy, because a session loads both.
func TestHookPairingSpansBothScopes(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"cfo hook turnend-guard"}]}]}}`)
	t.Setenv("CFO_HOME", dir)
	configDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	if err := os.WriteFile(filepath.Join(configDir, "settings.json"), []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"cfo hook stop-autoarm"}]}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if c := checkHookPairing(); c.Err != "" {
		t.Errorf("Err = %q, want empty when the pair is split across the two scopes", c.Err)
	}
}

// stopHookSettings renders a settings.json registering commands as Stop
// hooks, through json.Marshal so the commands carry the same escape
// sequences a real settings file gives them.
func stopHookSettings(t *testing.T, commands ...string) string {
	t.Helper()
	entries := []any{}
	for _, command := range commands {
		entries = append(entries, map[string]any{"type": "command", "command": command})
	}
	return stopHookEntries(t, entries...)
}

func stopHookEntries(t *testing.T, entries ...any) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{"hooks": map[string]any{"Stop": []any{map[string]any{"hooks": entries}}}})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestHookPairingRecognizesInstalledCommands drives the check with the exact
// entries `cfo install` writes: the home's cfo.exe run with args hook and a
// name, which no settings string spells as `cfo hook <name>`. A check that
// only knew the hand-written form stays silent on exactly the wiring the
// installer produces.
func TestHookPairingRecognizesInstalledCommands(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CFO_HOME", dir)
	entries := map[string]any{}
	for _, hook := range install.Hooks(dir) {
		entries[hook.Name] = map[string]any{"type": "command", "command": hook.Command, "args": hook.Args}
	}

	writeSettings(t, dir, stopHookEntries(t, entries["turnend-guard"]))
	if c := checkHookPairing(); c.Err == "" {
		t.Error("Err = empty, want non-empty with the installed guard registered alone")
	}

	writeSettings(t, dir, stopHookEntries(t, entries["turnend-guard"], entries["stop-autoarm"]))
	if c := checkHookPairing(); c.Err != "" {
		t.Errorf("Err = %q, want empty with both installed hooks registered", c.Err)
	}
}

// A machine not yet installed again still holds the shell-form commands an
// earlier install wrote, whose quotes arrive JSON-escaped in the raw file
// text; the check reads them as the installer does.
func TestHookPairingRecognizesShellFormCommands(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CFO_HOME", dir)
	shellForm := `CFO_ROOT="${CFO_HOME:-$CLAUDE_PROJECT_DIR}"; [ -x "$CFO_ROOT"/cfo.exe ] || exit 0; "$CFO_ROOT"/cfo.exe hook `

	writeSettings(t, dir, stopHookSettings(t, shellForm+"turnend-guard"))
	if c := checkHookPairing(); c.Err == "" {
		t.Error("Err = empty, want non-empty with the shell-form guard registered alone")
	}

	writeSettings(t, dir, stopHookSettings(t, shellForm+"turnend-guard", shellForm+"stop-autoarm"))
	if c := checkHookPairing(); c.Err != "" {
		t.Errorf("Err = %q, want empty with both shell-form hooks registered", c.Err)
	}
}

// TestMain keeps this suite off the machine's own fleet. On a host where the
// CFO is installed, CFO_HOME, CFO_STATE_OVERRIDE, and a real ~/.claude are
// all in the environment, and a test that resolves any of them writes into
// the live wake queue - which is not a hypothetical: it is how this guard
// came to be written.
func TestMain(m *testing.M) {
	if strings.EqualFold(filepath.Base(os.Args[0]), "claude.exe") {
		fmt.Println(fakeClaudeVersion)
		os.Exit(0)
	}
	for _, name := range []string{"CFO_HOME", "CFO_STATE_OVERRIDE", "CFO_ROLE"} {
		if err := os.Unsetenv(name); err != nil {
			panic(err)
		}
	}
	configDir, err := os.MkdirTemp("", "cfo-test-claude-config-")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("CLAUDE_CONFIG_DIR", configDir); err != nil {
		panic(err)
	}
	code := m.Run()
	os.RemoveAll(configDir)
	os.Exit(code)
}
