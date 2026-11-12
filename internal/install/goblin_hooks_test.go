package install

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// A goblin's tool call started a goblins process for its hooks, and a shell call
// two, the seal before it and the native hook with the key alarm after it:
// 157 of the 940 processes one goblin started in 11 minutes on 2026-10-27
// (#1610). Both post to the board now, which does their work in its own
// process, so no tool call starts one. Each carries the goblin's variables
// the board reads, and its terminal's id and proof, and a seal the board
// does not answer refuses the command, as a seal that cannot be read does.
func TestAGoblinsToolCallsStartNoProcessForItsHooks(t *testing.T) {
	// Arrange
	root := t.TempDir()
	board := "http://127.0.0.1:4310"
	settings, err := GoblinSettings(root, filepath.Join(root, "state"), board)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "goblin-claude-settings.json")
	if err := os.WriteFile(path, settings, 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	hooks := registeredHooks(t, path)

	// Assert
	want := map[string]registeredHook{
		"PreToolUse":  {Event: "PreToolUse", Matcher: GoblinSealMatcher, URL: board + GoblinSealPath, Timeout: KeyAlarmSeconds, OnFailure: "block"},
		"PostToolUse": {Event: "PostToolUse", URL: board + GoblinToolPath, Timeout: KeyAlarmSeconds},
	}
	for _, event := range []string{"PreToolUse", "PostToolUse"} {
		entries := slices.DeleteFunc(slices.Clone(hooks), func(hook registeredHook) bool { return hook.Event != event })
		if expected := want[event]; len(entries) != 1 || entries[0].Matcher != expected.Matcher || entries[0].URL != expected.URL || entries[0].Command != "" || entries[0].Timeout != expected.Timeout || entries[0].OnFailure != expected.OnFailure {
			t.Fatalf("%s hooks = %+v, want only %+v", event, entries, expected)
		}
		entry := entries[0]
		if !slices.Equal(entry.AllowedEnvVars, HookVariables) || len(entry.Headers) != len(HookVariables) {
			t.Errorf("%s hook may read %q into %v, want each of %q in its own header", event, entry.AllowedEnvVars, entry.Headers, HookVariables)
		}
		for _, variable := range HookVariables {
			if got := entry.Headers[HookHeader(variable)]; got != "${"+variable+"}" {
				t.Errorf("%s hook header %s = %q, want ${%s}", event, HookHeader(variable), got, variable)
			}
		}
	}
	processes := func(tool string) int {
		total := 0
		for _, hook := range hooks {
			if (hook.Event == "PreToolUse" || hook.Event == "PostToolUse") && hook.URL == "" && selects(t, hook.Matcher, tool) {
				total++
			}
		}
		return total
	}
	for _, tool := range []string{"Bash", "PowerShell", "Monitor", "Read", "Edit", "Grep", "WebFetch"} {
		if got := processes(tool); got != 0 {
			t.Errorf("a %s call starts %d processes for the goblin's hooks, want none", tool, got)
		}
	}
}
