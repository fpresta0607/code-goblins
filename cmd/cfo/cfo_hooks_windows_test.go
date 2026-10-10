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
