package supervisor

import (
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/harness"
)

const updateRule = "────────────────────────────────────────────────────────────"

// claudeAtPrompt is Claude Code at rest: its composer between two rules
// holding text, and its footer under them.
func claudeAtPrompt(composer string, footer ...string) []string {
	return append([]string{"● Pushed the branch.", updateRule, "❯ " + composer, updateRule}, footer...)
}

// A harness waits for a restart onto an update when its own screen says so,
// or when its program was installed after it started; an install before it
// started is the one it runs, and a start that is not known proves nothing.
func TestAHarnessUpdateIsSeenOnItsScreenOrByAnInstallSinceItStarted(t *testing.T) {
	started := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	quiet := claudeAtPrompt("", "  ⏵⏵ auto mode on (shift+tab to cycle)")
	said := claudeAtPrompt("", "  ⏵⏵ auto mode on (shift+tab to cycle)", "  ✓ Update installed · Restart to update")
	for name, test := range map[string]struct {
		kind               harness.Kind
		screen             []string
		started, installed time.Time
		want               HarnessUpdate
		waits              bool
	}{
		"its own line":                 {harness.Claude, said, started, started.Add(-time.Hour), HarnessUpdate{Harness: "claude", Line: "✓ Update installed · Restart to update"}, true},
		"installed since it started":   {harness.Codex, nil, started, started.Add(time.Hour), HarnessUpdate{Harness: "codex", Installed: started.Add(time.Hour)}, true},
		"both":                         {harness.Claude, said, started, started.Add(time.Hour), HarnessUpdate{Harness: "claude", Line: "✓ Update installed · Restart to update", Installed: started.Add(time.Hour)}, true},
		"installed before it started":  {harness.Claude, quiet, started, started.Add(-time.Hour), HarnessUpdate{Harness: "claude"}, false},
		"a start that is not known":    {harness.Pi, nil, time.Time{}, started, HarnessUpdate{Harness: "pi"}, false},
		"a harness not on PATH at all": {harness.Claude, quiet, started, time.Time{}, HarnessUpdate{Harness: "claude"}, false},
	} {
		t.Run(name, func(t *testing.T) {
			update, waits := harnessUpdate(test.kind, test.screen, test.started, test.installed)
			if waits != test.waits || update != test.want {
				t.Errorf("harnessUpdate = %+v, %v; want %+v, %v", update, waits, test.want, test.waits)
			}
		})
	}
}

// A harness is at a stopping point when its turn has ended: its Stop hook
// waits on the queue, which a Claude Code CFO's does after every turn while
// goblins work, however its screen draws that, or no turn, tool or background
// work runs and its composer is empty. A dialog, a turn, a background shell
// or text left unsent is not one.
func TestAHarnessIsAtAStoppingPointOnlyOnceItsTurnHasEnded(t *testing.T) {
	screens, _ := harness.NativeScreens(harness.Claude)
	idle := claudeAtPrompt("", "  ⏵⏵ auto mode on (shift+tab to cycle)")
	turn := append([]string{"✻ Reticulating… (12s · esc to interrupt)"}, idle...)
	for name, test := range map[string]struct {
		screen        []string
		isHookWaiting bool
		want          bool
	}{
		"idle at an empty prompt":          {idle, false, true},
		"in a turn":                        {turn, false, false},
		"its Stop hook waits":              {turn, true, true},
		"a background shell runs":          {claudeAtPrompt("", "  ⏵⏵ auto mode on · 1 shell"), false, false},
		"text left unsent":                 {claudeAtPrompt("one more thing", "  ⏵⏵ auto mode on"), false, false},
		"a dialog":                         {[]string{"Do you trust the files in this folder?", "> Yes, I trust this folder"}, true, false},
		"no composer to read":              {[]string{"● Pushed the branch."}, false, false},
		"its Stop hook waits, text unsent": {claudeAtPrompt("one more thing"), true, true},
	} {
		t.Run(name, func(t *testing.T) {
			if got := atStoppingPoint(screens, test.screen, test.isHookWaiting); got != test.want {
				t.Errorf("atStoppingPoint(%q, hook %v) = %v, want %v", test.screen, test.isHookWaiting, got, test.want)
			}
		})
	}
}
