package main

import (
	"strings"
	"testing"
	"time"
)

// A codex guard started without the state folder, the task or the time the
// allowance renews, as no armed guard starts it, refuses all the same and
// names its usage.
func TestCodexGuardWithoutItsArgumentsStillStartsNothing(t *testing.T) {
	for name, args := range map[string][]string{
		"no arguments":    nil,
		"no state":        {"--task", "g1", "--until", "2026-10-22T12:00:13Z", "--", "exec"},
		"no renewal":      {"--state", t.TempDir(), "--task", "g1", "--", "exec"},
		"a bad renewal":   {"--state", t.TempDir(), "--task", "g1", "--until", "Thursday", "--", "exec"},
		"an unknown flag": {"--yolo"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			var stderr strings.Builder

			// Act
			code := runCodexGuard(args, &stderr, time.Now())

			// Assert
			if code != 2 || !strings.Contains(stderr.String(), "usage: goblins codex-guard") {
				t.Errorf("goblins codex-guard %q = %d, %q, want 2 and its usage", args, code, stderr.String())
			}
		})
	}
}
