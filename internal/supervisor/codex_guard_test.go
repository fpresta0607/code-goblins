package supervisor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/quota"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// codexReading is a quota reading in which Codex's window of kind is used
// percent used and renews at reset.
func codexReading(kind string, used float64, reset time.Time) quota.Report {
	return quota.Report{Providers: map[string]quota.Provider{"codex": {
		Known:   true,
		Windows: []quota.Window{{ID: kind, Kind: kind, WindowSeconds: 604800, PercentUsed: used, ResetsAt: reset}},
		Scopes:  map[string]quota.Scope{"all_models": {Name: "all_models", Known: true, BoundedBy: []string{kind}}},
	}}}
}

// guardedCodex is what task id's codex guard folder in h's state holds for
// cmd, empty when it holds none.
func guardedCodex(t *testing.T, stateDir, id string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(stateDir, "codex-guard", id, "codex.cmd"))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// While Codex's week is used up, the allowance pass arms the codex guard of a
// goblin on Claude Code, whose own allowance is not Codex's, so a codex its
// terminal starts is refused rather than billed: on 2026-10-16 and 17 such
// goblins started eight Codex sessions that drew paid credits.
func TestCodexsUsedUpWeekArmsTheCodexGuardOfAGoblinOnClaudeCode(t *testing.T) {
	// Arrange
	service, h := fleetService(t)
	liveGoblin(t, h, "g1", filepath.Join(t.TempDir(), "project"))
	now := time.Now().UTC().Truncate(time.Second)
	reset := now.Add(5 * 24 * time.Hour)
	service.Options.Quota = func(context.Context) (quota.Report, string) { return codexReading("weekly", 100, reset), "" }

	// Act
	err := service.pauseAtAllowanceFloor(t.Context(), &fleetWakes{}, now)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	stub := guardedCodex(t, h.State, "g1")
	for _, want := range []string{`"codex-guard"`, `"--task" "g1"`, `"--until" "` + reset.Format(time.RFC3339) + `"`, filepath.Join(h.Programs(), "cfo.exe")} {
		if !strings.Contains(stub, want) {
			t.Errorf("g1's guard runs %q, want it to hold %s", stub, want)
		}
	}
	if _, err := os.Stat(filepath.Join(h.State, "codex-guard", "g1", "codex")); err != nil {
		t.Errorf("g1's guard holds no codex for Git Bash: %v", err)
	}
}

// The guard follows the last reading that measured Codex: armed while a
// window bounding Codex is used up, disarmed once Codex has its allowance or
// the window renews, never armed for a task authorized to spend credits, and
// gone for a task that is no longer live. A reading skipped or stale keeps
// the last word until the window it named renews.
func TestTheCodexGuardFollowsTheLastReadingOfCodex(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	reset := now.Add(5 * 24 * time.Hour)
	for _, test := range []struct {
		name           string
		reading        quota.Report
		skipped        string
		lastWord       time.Time
		isAuthorized   bool
		shouldBeArmed  bool
		shouldKeepWord bool
	}{
		{name: "Codex has its allowance", reading: codexReading("weekly", 60, reset)},
		{name: "Codex's week is used up", reading: codexReading("weekly", 100, reset), shouldBeArmed: true, shouldKeepWord: true},
		{name: "Codex's session is used up", reading: codexReading("session", 100, now.Add(time.Hour)), shouldBeArmed: true, shouldKeepWord: true},
		{name: "the task may spend credits", reading: codexReading("weekly", 100, reset), isAuthorized: true, shouldKeepWord: true},
		{name: "a skipped reading keeps the last word", skipped: "quota-axi did not answer", lastWord: reset, shouldBeArmed: true, shouldKeepWord: true},
		{name: "a stale reading keeps the last word", reading: quota.Report{Providers: map[string]quota.Provider{"codex": {Known: true, Stale: true}}}, lastWord: reset, shouldBeArmed: true, shouldKeepWord: true},
		{name: "a reading without Codex keeps the last word", reading: quota.Report{Providers: map[string]quota.Provider{}}, lastWord: reset, shouldBeArmed: true, shouldKeepWord: true},
		{name: "the last word renewed", skipped: "quota-axi did not answer", lastWord: now.Add(-time.Minute)},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			service, h := fleetService(t)
			project := filepath.Join(t.TempDir(), "project")
			liveGoblin(t, h, "g1", project)
			if err := state.WriteTaskCodexCredits(h.State, "g1", test.isAuthorized); err != nil {
				t.Fatal(err)
			}
			// A folder an earlier reading armed, and one of a task gone since.
			for _, id := range []string{"g1", "gone-1"} {
				writeFile(t, filepath.Join(h.State, "codex-guard", id, "codex.cmd"), "@echo armed\r\n")
			}
			service.Options.Quota = func(context.Context) (quota.Report, string) { return test.reading, test.skipped }
			watched := &fleetWakes{CodexUsedUpUntil: test.lastWord}

			// Act
			err := service.pauseAtAllowanceFloor(t.Context(), watched, now)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if isArmed := strings.Contains(guardedCodex(t, h.State, "g1"), "codex-guard"); isArmed != test.shouldBeArmed {
				t.Errorf("g1's guard armed = %v, want %v", isArmed, test.shouldBeArmed)
			}
			if _, err := os.Stat(filepath.Join(h.State, "codex-guard", "gone-1")); !os.IsNotExist(err) {
				t.Errorf("the guard folder of a task no longer live is still there (%v)", err)
			}
			if hasWord := !watched.CodexUsedUpUntil.IsZero(); hasWord != test.shouldKeepWord {
				t.Errorf("the pass keeps Codex used up until %s, want a word kept %v", watched.CodexUsedUpUntil, test.shouldKeepWord)
			}
		})
	}
}
