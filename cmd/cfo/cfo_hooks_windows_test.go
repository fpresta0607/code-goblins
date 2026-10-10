package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/install"
)

// Until 2026-10 the CFO's hooks were in the user's settings, where every
// Claude Code session on the machine ran them: each Bash call, turn end and
// start started cfo.exe, about 0.1 s each, for a hook that left on its
// environment. A Claude Code CFO's terminal starts with them instead: its
// harness is handed --settings and a file in the home's state folder that
// holds every CFO hook, each running the home's own cfo.exe.
func TestTheCFOsTerminalStartsClaudeCodeWithItsHooks(t *testing.T) {
	// Arrange
	h := scratchHome(t)

	// Act
	startCFOSession(t, h)

	// Assert
	raw, err := os.ReadFile(filepath.Join(h.Root, fakeClaudeArguments))
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(string(raw), "\n")
	settings := filepath.Join(h.State, "cfo-claude-settings.json")
	if len(args) < 2 || args[0] != "--settings" || args[1] != settings {
		t.Fatalf("the CFO's harness started with %q, want it to open with --settings %s", args, settings)
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
			t.Errorf("the CFO's terminal runs %q for hook %s, want %s", commands[hook.Name], hook.Name, hook.Command)
		}
	}
}

// cfo doctor says whether the user's Claude Code settings still hold the
// CFO's hooks, which every session of the user would run, and names the fix.
func TestRunDoctorSaysWhetherTheUsersSettingsStillHoldTheCFOsHooks(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	older, err := install.CFOSettings(home)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, settings, want string
	}{
		{"no settings file", "", "hooks: the CFO's terminal starts with its hooks, and %s holds none of them, so no other Claude Code session runs one\n"},
		{"only the user's own hooks", `{"hooks": {"Stop": [{"hooks": [{"type": "command", "command": "node session-end.js"}]}]}}`, "hooks: the CFO's terminal starts with its hooks, and %s holds none of them"},
		{"an older build's install", string(older), "hooks: %s still holds 6 of the CFO's hooks (pre-compact, pretool-bash, pretool-subagent, session-start, stop-autoarm, turnend-guard), so every Claude Code session on this machine starts cfo.exe for them on each Bash call, turn end and start; the CFO's terminal starts with its own, and `cfo install` takes these out"},
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
