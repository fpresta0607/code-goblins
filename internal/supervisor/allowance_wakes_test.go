package supervisor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/quota"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// runningGoblin records a native goblin on harness whose terminal's host is
// this test's own process, so it reads as running.
func runningGoblin(t *testing.T, h home.Home, id, harness string) {
	t.Helper()
	meta := state.TaskMeta{ID: id, Backend: "native", Harness: harness, Model: "claude-opus-5-5", SpawnGen: "generation-" + id}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(host.Record{ID: id, HostPID: os.Getpid(), Started: time.Now().Add(time.Second), Pipe: "fixture", Token: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(h.State, "hosts", id+".json"), string(data))
}

// allowancePass runs one fleet reading's allowance pass on report at now.
func allowancePass(t *testing.T, s *Service, watched *fleetWakes, report quota.Report, now time.Time) {
	t.Helper()
	s.Options.Quota = func(context.Context) (quota.Report, string) { return report, "" }
	if err := s.pauseAtAllowanceFloor(t.Context(), watched, now); err != nil {
		t.Fatal(err)
	}
}

// The fixture is quota-axi's snapshot of 2026-10-07 23:13Z for Claude and
// Codex, with Claude's five-hour window set to used up.
func TestSpentSessionWakesTheCFOOnceAtItsResetNamingTheGoblinsOnIt(t *testing.T) {
	// Arrange
	data, err := os.ReadFile(filepath.Join("..", "quota", "testdata", "session-spent.json"))
	if err != nil {
		t.Fatal(err)
	}
	seen := time.Date(2026, 10, 7, 23, 14, 0, 0, time.UTC)
	report, err := quota.Parse(data, seen)
	if err != nil {
		t.Fatal(err)
	}
	reset := report.Providers["claude"].Resets["five_hour"]
	s, h := fleetService(t)
	s.Options.Dispatch = &Dispatch{Spawn: func(context.Context, []string) (string, error) {
		t.Fatal("a used-up session is waited out, never paused")
		return "", nil
	}}
	runningGoblin(t, h, "claude-goblin", "claude")
	runningGoblin(t, h, "codex-goblin", "codex")
	watched := &fleetWakes{}

	// Act
	allowancePass(t, s, watched, report, seen)
	beforeReset := fleetWakeRecords(t, h, "allowance")
	allowancePass(t, s, watched, report, reset.Add(-time.Second))
	stillBefore := fleetWakeRecords(t, h, "allowance")
	allowancePass(t, s, watched, report, reset)
	allowancePass(t, s, watched, report, reset.Add(time.Minute))
	records := fleetWakeRecords(t, h, "allowance")

	// Assert
	if len(beforeReset) != 1 || len(stillBefore) != 1 || !strings.Contains(beforeReset[0].Detail, "five_hour window is 100 percent used") {
		t.Fatalf("before the reset the CFO hears only the warning: %+v then %+v", beforeReset, stillBefore)
	}
	if len(records) != 2 {
		t.Fatalf("allowance wakes=%+v, want the warning and one at the reset", records)
	}
	renewed := records[1]
	if renewed.Key != "claude" || !strings.Contains(renewed.Detail, "renewed at "+reset.Format(time.RFC3339)) || !strings.Contains(renewed.Detail, "claude-goblin") || strings.Contains(renewed.Detail, "codex-goblin") {
		t.Fatalf("the wake at the reset names the Claude goblins that stopped on it: %+v", renewed)
	}
	for _, record := range records {
		if strings.Contains(strings.ToLower(record.Detail), "limit") {
			t.Fatalf("a wake the CFO's terminal shows must not read as its harness refusing: %q", record.Detail)
		}
	}
}

func TestSpentSessionHoldsStartsAndResumesOnItsHarnessUntilItsReset(t *testing.T) {
	for _, test := range []struct {
		name         string
		sessionUsed  float64
		at           time.Duration
		shouldBeHeld bool
		harness      string
	}{
		{name: "used up", sessionUsed: 100, harness: "claude", shouldBeHeld: true},
		{name: "used up, at its reset", sessionUsed: 100, at: time.Hour, harness: "claude"},
		{name: "nearly used up", sessionUsed: 99.9, harness: "claude"},
		{name: "used up on another harness", sessionUsed: 100, harness: "codex"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			now := time.Date(2026, 10, 7, 23, 14, 0, 0, time.UTC)
			report := quota.Report{Providers: map[string]quota.Provider{"claude": {Known: true, Windows: []quota.Window{
				{ID: "five_hour", Kind: "session", WindowSeconds: 18000, PercentUsed: test.sessionUsed, ResetsAt: now.Add(time.Hour)},
				{ID: "seven_day", Kind: "weekly", WindowSeconds: 604800, PercentUsed: 26, ResetsAt: now.Add(5 * 24 * time.Hour)},
			}, Scopes: map[string]quota.Scope{"all_models": {Name: "all_models", Known: true, BoundedBy: []string{"five_hour", "seven_day"}}}}}}
			s, _ := fleetService(t)
			watched := &fleetWakes{}

			// Act
			allowancePass(t, s, watched, report, now.Add(test.at))

			// Assert
			if isHeld := allowanceBlocked(watched, test.harness, "", now.Add(test.at)); isHeld != test.shouldBeHeld {
				t.Fatalf("held=%v, want %v", isHeld, test.shouldBeHeld)
			}
		})
	}
}

func TestAllowanceWarningWakesOnceWhenAWindowPasses85PercentWhileGoblinsRunOnIt(t *testing.T) {
	now := time.Date(2026, 10, 7, 23, 14, 0, 0, time.UTC)
	for _, test := range []struct {
		name          string
		used          float64
		windowID      string
		goblinHarness string
		isStale       bool
		resetIn       time.Duration
		shouldWake    bool
	}{
		{name: "84.9 percent", used: 84.9, windowID: "seven_day", goblinHarness: "claude", resetIn: time.Hour},
		{name: "85 percent", used: 85, windowID: "seven_day", goblinHarness: "claude", resetIn: time.Hour, shouldWake: true},
		{name: "97 percent of the session", used: 97, windowID: "five_hour", goblinHarness: "claude", resetIn: time.Hour, shouldWake: true},
		{name: "no goblin on the harness", used: 97, windowID: "seven_day", goblinHarness: "codex", resetIn: time.Hour},
		{name: "a window the goblin's model is not bound by", used: 97, windowID: "model:fable", goblinHarness: "claude", resetIn: time.Hour},
		{name: "stale reading", used: 97, windowID: "seven_day", goblinHarness: "claude", isStale: true, resetIn: time.Hour},
		{name: "window already renewed", used: 97, windowID: "seven_day", goblinHarness: "claude"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			windows := []quota.Window{
				{ID: "five_hour", Kind: "session", WindowSeconds: 18000, PercentUsed: 10, ResetsAt: now.Add(time.Hour)},
				{ID: "seven_day", Kind: "weekly", WindowSeconds: 604800, PercentUsed: 10, ResetsAt: now.Add(time.Hour)},
				{ID: "model:fable", Kind: "model", WindowSeconds: 604800, PercentUsed: 10, ResetsAt: now.Add(time.Hour)},
			}
			for index := range windows {
				if windows[index].ID == test.windowID {
					windows[index].PercentUsed, windows[index].ResetsAt = test.used, now.Add(test.resetIn)
				}
			}
			report := quota.Report{Providers: map[string]quota.Provider{"claude": {Known: true, Stale: test.isStale, Windows: windows, Scopes: map[string]quota.Scope{
				"all_models":  {Name: "all_models", Known: true, BoundedBy: []string{"five_hour", "seven_day"}},
				"model:fable": {Name: "model:fable", Known: true, BoundedBy: []string{"five_hour", "seven_day", "model:fable"}},
			}}}}
			s, h := fleetService(t)
			runningGoblin(t, h, "goblin", test.goblinHarness)
			watched := &fleetWakes{}

			// Act
			allowancePass(t, s, watched, report, now)
			allowancePass(t, s, watched, report, now.Add(time.Minute))

			// Assert
			records := fleetWakeRecords(t, h, "allowance")
			if !test.shouldWake {
				if len(records) != 0 {
					t.Fatalf("woke: %+v", records)
				}
				return
			}
			if len(records) != 1 || records[0].Key != "claude" || !strings.Contains(records[0].Detail, test.windowID+" window is") || !strings.Contains(records[0].Detail, "goblin") {
				t.Fatalf("allowance wakes=%+v, want one naming the window and the goblin", records)
			}
		})
	}
}

func TestAllowanceWarningWakesAgainOnlyForTheNextWindow(t *testing.T) {
	// Arrange
	now := time.Date(2026, 10, 7, 23, 14, 0, 0, time.UTC)
	window := func(reset time.Time) quota.Report {
		return quota.Report{Providers: map[string]quota.Provider{"codex": {Known: true, Windows: []quota.Window{
			{ID: "weekly", Kind: "weekly", WindowSeconds: 604800, PercentUsed: 90, ResetsAt: reset},
		}, Scopes: map[string]quota.Scope{"all_models": {Name: "all_models", Known: true, BoundedBy: []string{"weekly"}}}}}}
	}
	s, h := fleetService(t)
	runningGoblin(t, h, "goblin", "codex")
	watched := &fleetWakes{}
	firstReset := now.Add(time.Hour)

	// Act: quota-axi's reset times differ by microseconds between readings.
	allowancePass(t, s, watched, window(firstReset), now)
	allowancePass(t, s, watched, window(firstReset.Add(23*time.Microsecond)), now.Add(time.Minute))
	allowancePass(t, s, watched, window(firstReset.Add(7*24*time.Hour)), firstReset.Add(time.Minute))

	// Assert
	if records := fleetWakeRecords(t, h, "allowance"); len(records) != 2 {
		t.Fatalf("allowance wakes=%+v, want one for each window", records)
	}
}
