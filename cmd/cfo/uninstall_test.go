package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// On a machine of its own, an install followed by goblins uninstall --yes
// leaves no home, no environment and nothing in the harnesses' folders, and
// keeps the person's fleet data in the folder it names. Its dry run before
// changes nothing.
func TestUninstallRemovesTheHomeAndKeepsTheFleetData(t *testing.T) {
	// Arrange
	root, profile, envFile := installOnAScratchMachine(t, t.TempDir())
	write := func(path, content string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(root, "data", "backlog.md"), "# Backlog\n")
	var dry, dryErr bytes.Buffer
	if code := runUninstall([]string{"--dry-run", "--json"}, strings.NewReader(""), &dry, &dryErr); code != 0 {
		t.Fatalf("goblins uninstall --dry-run --json exited %d:\n%s", code, dryErr.String())
	}
	var plan struct {
		Home  string `json:"home"`
		Kept  string `json:"kept"`
		Items []struct {
			Action string `json:"action"`
			Group  string `json:"group"`
			What   string `json:"what"`
			Where  string `json:"where"`
		} `json:"items"`
	}
	if err := json.Unmarshal(dry.Bytes(), &plan); err != nil {
		t.Fatalf("the dry run printed no plan: %v\n%s", err, dry.String())
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("the dry run took the home: %v", err)
	}

	// Act
	var stdout, stderr bytes.Buffer
	code := runUninstall([]string{"--yes"}, strings.NewReader(""), &stdout, &stderr)

	// Assert
	if code != 0 {
		t.Fatalf("goblins uninstall --yes exited %d:\n%s%s", code, stdout.String(), stderr.String())
	}
	said := stdout.String()
	if !strings.Contains(said, "Checked: nothing of Code Goblins is left on this PC.") || strings.ContainsAny(said, ";\u2014") {
		t.Errorf("the uninstall said:\n%s\nwant it checked nothing is left, with no semicolon or dash", said)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Errorf("the home %s is still there: %v", root, err)
	}
	if data, err := os.ReadFile(filepath.Join(plan.Kept, "fleet", "data", "backlog.md")); err != nil || string(data) != "# Backlog\n" {
		t.Errorf("the kept backlog = %q, %v; want it in %s", data, err, plan.Kept)
	}
	if env, err := os.ReadFile(envFile); err == nil && (strings.Contains(string(env), "CFO_HOME") || strings.Contains(string(env), root)) {
		t.Errorf("the user environment still names the home:\n%s", env)
	}
	for _, gone := range []string{filepath.Join(profile, ".claude", "skills", "lavish"), filepath.Join(profile, ".agents", "skills", "lavish"), filepath.Join(profile, ".claude", "settings.json")} {
		if _, err := os.Lstat(gone); !os.IsNotExist(err) {
			t.Errorf("%s is still there: %v", gone, err)
		}
	}
	var again bytes.Buffer
	if code := runUninstall([]string{"--yes"}, strings.NewReader(""), &again, &stderr); code != 0 || !strings.Contains(again.String(), "nothing to remove") {
		t.Errorf("a second uninstall exited %d and said %q, want nothing to remove", code, again.String())
	}
}

// Without --yes the uninstall asks, and anything but yes changes nothing.
func TestUninstallChangesNothingUnlessThePersonSaysYes(t *testing.T) {
	// Arrange
	root, _, _ := installOnAScratchMachine(t, t.TempDir())

	// Act
	var stdout, stderr bytes.Buffer
	code := runUninstall(nil, strings.NewReader("no\n"), &stdout, &stderr)

	// Assert
	if code != 1 || !strings.Contains(stdout.String(), "Type yes") || !strings.Contains(stdout.String(), "Nothing was changed.") {
		t.Errorf("exited %d and said:\n%s%s\nwant it to ask, then change nothing", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(root, "bin", "cfo.exe")); err != nil {
		t.Errorf("the home lost its program although the person said no: %v", err)
	}
}

// A Code Goblins terminal stops with the uninstall, so the uninstall refuses
// to run in one.
func TestUninstallRefusesToRunInACodeGoblinsTerminal(t *testing.T) {
	// Arrange
	t.Setenv("CFO_HOST_ID", "cfo")

	// Act
	var stdout, stderr bytes.Buffer
	code := runUninstall([]string{"--yes"}, strings.NewReader(""), &stdout, &stderr)

	// Assert
	if code != 1 || !strings.Contains(stderr.String(), "terminal of your own") {
		t.Errorf("exited %d and said %q, want a refusal naming a terminal of the person's own", code, stderr.String())
	}
}
