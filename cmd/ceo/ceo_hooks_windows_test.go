package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/install"
)

// Until 2026-10 the CEO's hooks were in the user's settings, where every
// Claude Code session on the machine ran them: each Bash call, turn end and
// start started ceo.exe, about 0.1 s each, for a hook that left on its
// environment. A Claude Code CEO's terminal starts with them instead: its
// harness is handed --settings and a file in the home's state folder that
// holds every CEO hook, each running the home's own ceo.exe.
func TestTheCEOsTerminalStartsClaudeCodeWithItsHooks(t *testing.T) {
	// Arrange
	h := scratchHome(t)

	// Act
	startCEOSession(t, h)

	// Assert
	raw, err := os.ReadFile(filepath.Join(h.Root, fakeClaudeArguments))
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(string(raw), "\n")
	settings := filepath.Join(h.State, "ceo-claude-settings.json")
	if len(args) < 2 || args[0] != "--settings" || args[1] != settings {
		t.Fatalf("the CEO's harness started with %q, want it to open with --settings %s", args, settings)
	}
	data, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Hooks map[string][]struct {
			Hooks []map[string]any `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("%s is not a settings document: %v\n%s", settings, err, data)
	}
	commands := map[string]string{}
	for _, groups := range document.Hooks {
		for _, group := range groups {
			for _, entry := range group.Hooks {
				if name, ok := install.HookName(entry); ok {
					commands[name], _ = entry["command"].(string)
				}
			}
		}
	}
	for _, hook := range install.Hooks(h.Root) {
		if commands[hook.Name] != hook.Command {
			t.Errorf("the CEO's terminal runs %q for hook %s, want %s", commands[hook.Name], hook.Name, hook.Command)
		}
	}
}

// ceo doctor says whether the user's Claude Code settings still hold the
// CEO's hooks, which every session of the user would run, and names the fix.
func TestRunDoctorSaysWhetherTheUsersSettingsStillHoldTheCEOsHooks(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	older, err := install.CEOSettings(home, filepath.Join(home, "state"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, hook := range install.Hooks(home) {
		names = append(names, hook.Name)
	}
	slices.Sort(names)
	held := fmt.Sprintf("%d of the CEO's hooks (%s)", len(names), strings.Join(names, ", "))
	for _, tc := range []struct {
		name, settings, want string
	}{
		{"no settings file", "", "hooks: the CEO's terminal starts with its hooks, and %s holds none of them, so no other Claude Code session runs one\n"},
		{"only the user's own hooks", `{"hooks": {"Stop": [{"hooks": [{"type": "command", "command": "node session-end.js"}]}]}}`, "hooks: the CEO's terminal starts with its hooks, and %s holds none of them"},
		{"an older build's install", string(older), "hooks: %s still holds " + held + ", so every Claude Code session on this machine starts ceo.exe for them on each Bash call, turn end and start; the CEO's terminal starts with its own, and `ceo install` takes these out"},
		{"malformed", `{"hooks": []}`, "hooks: %s unreadable ("},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			dir := t.TempDir()
			t.Setenv("CLAUDE_CONFIG_DIR", dir)
			settings := filepath.Join(dir, "settings.json")
			if tc.settings != "" {
				if err := os.WriteFile(settings, []byte(tc.settings), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var stdout strings.Builder

			// Act
			reportUserHooks(&stdout)

			// Assert
			if want := strings.ReplaceAll(tc.want, "%s", settings); !strings.Contains(stdout.String(), want) {
				t.Errorf("doctor lacks %q\n%s", want, stdout.String())
			}
		})
	}
}
