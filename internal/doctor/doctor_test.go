package doctor

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
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
	for _, name := range []string{"git", "gh", "claude", "herdr", "treehouse"} {
		fakeTool(t, dir, name, name+" version 1.0.0", 0)
	}
	t.Setenv("PATH", dir)
	t.Setenv("CFO_HOME", t.TempDir()) // no .claude/settings.json: hook-pairing passes
	checks := Run()
	if len(checks) != 6 {
		t.Fatalf("len = %d, want 6 (5 tools + hook-pairing)", len(checks))
	}
	if !Healthy(checks) {
		t.Errorf("Healthy = false with all tools present: %+v", checks)
	}
	if checks[0].Name != "git" || checks[0].Version != "git version 1.0.0" {
		t.Errorf("git check = %+v, want captured version line", checks[0])
	}
	if checks[5].Name != "hook-pairing" {
		t.Errorf("checks[5] = %+v, want hook-pairing", checks[5])
	}
}

func TestRunMissingToolCarriesHint(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"git", "gh", "claude", "herdr"} {
		fakeTool(t, dir, name, name+" ok", 0)
	}
	t.Setenv("PATH", dir) // no treehouse
	t.Setenv("CFO_HOME", t.TempDir())
	checks := Run()
	if Healthy(checks) {
		t.Error("Healthy = true with treehouse missing")
	}
	last := checks[4]
	if last.Name != "treehouse" || last.Err == "" || last.Hint == "" {
		t.Errorf("treehouse check = %+v, want Err and Hint set", last)
	}
}

func TestRunBrokenToolReportsFailure(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"gh", "claude", "herdr", "treehouse"} {
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
