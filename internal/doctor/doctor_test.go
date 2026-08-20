package doctor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/install"
)

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
	t.Setenv("PATH", dir)
	t.Setenv("CFO_HOME", t.TempDir()) // no .claude/settings.json: hook-pairing passes
	checks := Run()
	if len(checks) != 9 {
		t.Fatalf("len = %d, want 9 (8 tools + hook-pairing)", len(checks))
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
	if checks[8].Name != "hook-pairing" {
		t.Errorf("checks[8] = %+v, want hook-pairing", checks[8])
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

func TestRunBrokenHerdrReportsFailure(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"git", "gh", "tasks-axi", "quota-axi"} {
		fakeTool(t, dir, name, name+" ok", 0)
	}
	fakeTool(t, dir, "herdr", "boom", 1)
	t.Setenv("PATH", dir)
	t.Setenv("CFO_HOME", t.TempDir())

	checks := Run()
	if Healthy(checks) {
		t.Fatal("Healthy = true with herdr --version failing")
	}
	if checks[2].Name != "herdr" || checks[2].Err == "" {
		t.Errorf("herdr check = %+v, want version failure", checks[2])
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
	fakeTool(t, dir, "claude", "claude 1.0.0", 0)
	fakeTool(t, dir, "codex", "boom", 1)
	t.Setenv("PATH", dir)

	probes := ProbeHarnesses(context.Background())
	if len(probes) != 4 {
		t.Fatalf("len = %d, want 4 (every supported harness): %+v", len(probes), probes)
	}
	if probes[0].Name != "claude" || !probes[0].OK || probes[0].Detail != "claude 1.0.0" {
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
	data, err := json.Marshal(map[string]any{"hooks": map[string]any{"Stop": []any{map[string]any{"hooks": entries}}}})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestHookPairingRecognizesInstalledCommands drives the check with the exact
// command strings `cfo install` writes, whose quotes arrive JSON-escaped in
// the raw file text: a check that only knew the hand-written `cfo hook
// <name>` form, or that scanned raw text, stays silent on exactly the wiring
// the installer produces.
func TestHookPairingRecognizesInstalledCommands(t *testing.T) {
	commands := map[string]string{}
	for _, hook := range install.Hooks() {
		commands[hook.Name] = hook.Command
	}
	dir := t.TempDir()
	t.Setenv("CFO_HOME", dir)

	writeSettings(t, dir, stopHookSettings(t, commands["turnend-guard"]))
	if c := checkHookPairing(); c.Err == "" {
		t.Error("Err = empty, want non-empty with the installed guard command registered alone")
	}

	writeSettings(t, dir, stopHookSettings(t, commands["turnend-guard"], commands["stop-autoarm"]))
	if c := checkHookPairing(); c.Err != "" {
		t.Errorf("Err = %q, want empty with both installed commands registered", c.Err)
	}
}

// TestMain keeps this suite off the machine's own fleet. On a host where the
// CFO is installed, CFO_HOME, CFO_STATE_OVERRIDE, and a real ~/.claude are
// all in the environment, and a test that resolves any of them writes into
// the live wake queue - which is not a hypothetical: it is how this guard
// came to be written.
func TestMain(m *testing.M) {
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
