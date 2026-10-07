package harness

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// claudeScreen is Claude Code at its prompt: the conversation, the composer
// between two full-width rules, and footer under them.
func claudeScreen(conversation []string, footer ...string) []string {
	rule := "────────────────────────────────────────────────────────────"
	screen := append([]string(nil), conversation...)
	screen = append(screen, rule, "❯ ", rule)
	return append(screen, footer...)
}

// Claude Code says an installed update waits for a restart in its footer, in
// the words of the version it runs, and the second half is a copy its
// servers choose: read from Claude Code 2.1.292's own build, "✓ Update
// installed · Restart to update", "· /restart to apply" and "· Restart to
// apply". Any of them is seen, wherever the footer puts it.
func TestClaudeCodesUpdateLineIsSeenInEachWording(t *testing.T) {
	conversation := []string{"● Done. The branch is pushed."}
	for name, footer := range map[string][]string{
		"restart to update":   {"  ⏵⏵ bypass permissions on (shift+tab to cycle)", "  ✓ Update installed · Restart to update"},
		"slash restart":       {"  ⏵⏵ auto mode on (shift+tab to cycle)", "  ✓ Update installed · /restart to apply"},
		"restart to apply":    {"  ✓ Update installed · Restart to apply"},
		"beside the mode":     {"  ⏵⏵ auto mode on (shift+tab to cycle)                 ✓ Update installed · Restart to update"},
		"cut at the width":    {"  ? for shortcuts                                       ✓ Update installed · Restart to up…"},
		"with a version":      {"  ✓ Update installed 2.1.293 · Restart to apply"},
		"under a status line": {"  fpres@MeanMachine code-goblins (main)", "  ⏵⏵ auto mode on", "  ✓ Update installed · Restart to update"},
	} {
		t.Run(name, func(t *testing.T) {
			row, shown := UpdateNotice(Claude, claudeScreen(conversation, footer...))
			if !shown || row == "" {
				t.Errorf("UpdateNotice(footer %q) = %q, %v; want the update line seen", footer, row, shown)
			}
		})
	}
}

// Only Claude Code's own footer says it: the same words anywhere above its
// prompt are the conversation, such as the CFO quoting the line back, and a
// screen whose footer cannot be found says nothing. Codex and pi only say an
// update is available, which a restart does not install.
func TestAnUpdateLineIsSeenOnlyWhereTheHarnessDrawsIt(t *testing.T) {
	quoted := []string{"> the footer said \"✓ Update installed · Restart to update\", so restart the CFO"}
	for name, test := range map[string]struct {
		kind   Kind
		screen []string
	}{
		"quoted in the conversation": {Claude, claudeScreen(quoted, "  ⏵⏵ auto mode on (shift+tab to cycle)")},
		"no footer found":            {Claude, []string{"✓ Update installed · Restart to update", "❯ "}},
		"footer without it":          {Claude, claudeScreen(nil, "  ⏵⏵ bypass permissions on (shift+tab to cycle)")},
		"codex update available":     {Codex, []string{"  ✨ Update available! 0.154.0 -> 0.157.0", "  Run npm install -g @openai/codex to update.", "› Ask Codex to do anything"}},
		"pi new version available":   {Pi, []string{"New version 0.86.0 is available. Run pi update", "────────", "", "────────", "0.0%/1.0M (auto)"}},
		"an unknown harness":         {Kind("kimi"), claudeScreen(nil, "  ✓ Update installed · Restart to update")},
	} {
		t.Run(name, func(t *testing.T) {
			if row, shown := UpdateNotice(test.kind, test.screen); shown {
				t.Errorf("UpdateNotice(%s, %q) = %q, true; want none", test.kind, test.screen, row)
			}
		})
	}
}

// Each harness's program is the one its launch finds on PATH: Claude Code's
// native build as claude.exe, which its installer writes again on every
// update, and the npm script shims codex and pi run through, which every npm
// install of them writes again. Its last write is when the harness was last
// installed.
func TestProgramInstalledIsWhenItsProgramOnPathWasWritten(t *testing.T) {
	directory := t.TempDir()
	installed := time.Date(2026, 10, 6, 19, 10, 37, 0, time.UTC)
	for _, name := range []string{"claude.exe", "codex.cmd", "pi.cmd"} {
		path := filepath.Join(directory, name)
		if err := os.WriteFile(path, []byte("stand-in"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, installed, installed); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", directory)
	for kind, name := range map[Kind]string{Claude: "claude.exe", Codex: "codex.cmd", Pi: "pi.cmd"} {
		program, at, err := ProgramInstalled(kind)
		if err != nil || !filepath.IsAbs(program) || filepath.Base(program) != name || !at.Equal(installed) {
			t.Errorf("ProgramInstalled(%s) = %q, %s, %v; want %s written at %s", kind, program, at, err, name, installed)
		}
	}
}

// A harness that is not on PATH has no install to read, which is an error,
// never a time that could read as an update.
func TestProgramInstalledRefusesAHarnessNotOnPath(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if program, at, err := ProgramInstalled(Claude); err == nil || !at.IsZero() {
		t.Errorf("ProgramInstalled(claude) = %q, %s, %v; want an error and no time", program, at, err)
	}
}
