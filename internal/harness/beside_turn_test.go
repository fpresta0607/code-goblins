package harness

import "testing"

// The Overlord, 2026-11-11 (#2372): a command typed in Modern Chat while a
// turn runs acts as the terminal's does. Each harness runs some of its
// built-in commands at once beside a turn and holds or refuses the rest, so
// whoever types for it asks which a command is before holding it for the
// turn's end.
func TestRunsBesideTurnFollowsEachHarnessesOwnTable(t *testing.T) {
	cases := []struct {
		name string
		kind Kind
		text string
		want bool
	}{
		{"claude runs a dialog command at once", Claude, "/remote-control", true},
		{"claude runs its alias at once", Claude, "/rc", true},
		{"claude runs a command with arguments at once", Claude, "/model opus", true},
		{"claude runs a command that opens no dialog at once", Claude, "/version", true},
		{"claude queues compact for the turn's end", Claude, "/compact", false},
		{"claude queues a session's own command", Claude, "/deploy-check staging", false},
		{"claude runs config at once only in its fullscreen interface", Claude, "/config", false},
		{"claude runs config at once with words after it", Claude, "/config theme dark", true},
		{"claude runs its alias at once with words after it", Claude, "/settings theme dark", true},
		{"codex takes no command for words after it", Codex, "/config theme dark", false},
		{"a longer name that starts with a listed one is another command", Claude, "/models", false},
		{"codex runs status beside a task", Codex, "/status", true},
		{"codex refuses compact while a task runs", Codex, "/compact", false},
		{"codex takes a skill as words", Codex, "$deploy-check", false},
		{"pi runs compact at once", Pi, "/compact", true},
		{"pi waits to reload", Pi, "/reload", false},
		{"pi queues a prompt template", Pi, "/deploy-check", false},
		{"a bang command is held as before", Claude, "!git status", false},
		{"words are no command", Claude, "model", false},
		{"a bare slash names no command", Claude, "/", false},
		{"surrounding space is not part of the name", Claude, "  /status  ", true},
		{"a harness with no table runs none", Kind("gemini"), "/status", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Act
			got := RunsBesideTurn(c.kind, c.text)

			// Assert
			if got != c.want {
				t.Fatalf("RunsBesideTurn(%q, %q) = %v, want %v", c.kind, c.text, got, c.want)
			}
		})
	}
}
