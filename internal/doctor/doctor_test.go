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
	checks := Run()
	if len(checks) != 5 {
		t.Fatalf("len = %d, want 5", len(checks))
	}
	if !Healthy(checks) {
		t.Errorf("Healthy = false with all tools present: %+v", checks)
	}
	if checks[0].Name != "git" || checks[0].Version != "git version 1.0.0" {
		t.Errorf("git check = %+v, want captured version line", checks[0])
	}
}

func TestRunMissingToolCarriesHint(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"git", "gh", "claude", "herdr"} {
		fakeTool(t, dir, name, name+" ok", 0)
	}
	t.Setenv("PATH", dir) // no treehouse
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
	checks := Run()
	if Healthy(checks) {
		t.Error("Healthy = true with git --version failing")
	}
	if checks[0].Name != "git" || checks[0].Err == "" {
		t.Errorf("git check = %+v, want Err set", checks[0])
	}
}
