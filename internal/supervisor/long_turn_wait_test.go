package supervisor

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/verify"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// A goblin's own turn that has waited an hour in the heavy line wakes the CEO
// once, naming the run and its task, however many readings find it still
// waiting, and a reading that fails forgets none it woke for (#2416).
func TestAGoblinTurnWaitingAnHourWakesTheCEOOnceNamingIt(t *testing.T) {
	// Arrange
	s, h := fleetService(t)
	now := time.Date(2026, 11, 12, 15, 0, 0, 0, time.UTC)
	first := verify.Standing{Who: "npm run test:e2e in a worktree, task Poppy (site-goblins-download-choice)", PID: 4100, Since: now.Add(-70 * time.Minute)}
	second := verify.Standing{Who: "goblins verify fast, task Ivy (nm-per-run-agent-env)", PID: 4200, Since: now.Add(-62 * time.Minute)}
	var runs []verify.Standing
	var failure error
	s.Options.Dispatch = &Dispatch{LongTurnWaits: func(time.Time) ([]verify.Standing, error) { return runs, failure }}
	read := func(readings int) int {
		t.Helper()
		before := len(longTurnWaitWakes(t, h))
		for range readings {
			if err := s.checkFleet(t.Context(), now); err != nil {
				t.Fatal(err)
			}
			now = now.Add(time.Minute)
		}
		return len(longTurnWaitWakes(t, h)) - before
	}

	// Act
	runs = []verify.Standing{first}
	firstWakes := read(3)
	runs = []verify.Standing{first, second}
	secondWakes := read(3)
	runs, failure = nil, errors.New("custody record incomplete")
	failedWakes := read(1)
	runs, failure = []verify.Standing{first, second}, nil
	afterFailure := read(2)

	// Assert
	if firstWakes != 1 || secondWakes != 1 || failedWakes != 0 || afterFailure != 0 {
		t.Fatalf("wakes: %d for the first run, %d when a second joined it, %d at a failed reading, %d after it; want 1, 1, 0, 0", firstWakes, secondWakes, failedWakes, afterFailure)
	}
	detail := longTurnWaitWakes(t, h)[0].Detail
	for _, want := range []string{"heavy_line_turn_waits:", first.Who, "pid 4100", "1h10m0s", "goblins gate turns"} {
		if !strings.Contains(detail, want) {
			t.Errorf("wake %q lacks %q", detail, want)
		}
	}
}

// MachineLongTurnWaits reads from the machine's store only the goblins' own
// turns that have waited an hour: not a younger one, a gate's Test step or a
// run at the lowest priority, however long those have waited.
func TestMachineLongTurnWaitsReadsOnlyGoblinTurnsWaitingAnHour(t *testing.T) {
	// Arrange
	heavyLine(t, 1, 0)
	store, err := verify.AdmissionDir()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	standInLine(t, store, "00000000000000000001-1-1", "a goblin's own turn an hour old", map[string]bool{}, now.Add(-61*time.Minute))
	standInLine(t, store, "00000000000000000002-1-1", "a goblin's own turn just joined", map[string]bool{}, now.Add(-time.Minute))
	standInLine(t, store, "+00000000000000000003-1-1", "a gate's Test step", map[string]bool{"is_test_step": true}, now.Add(-90*time.Minute))
	standInLine(t, store, "!00000000000000000004-1-1", "a gate's Test step past the line's limit", map[string]bool{"is_test_step": true}, now.Add(-3*time.Hour))
	standInLine(t, store, "z-00000000000000000005-1-1", "a run at the lowest priority", map[string]bool{"is_low_priority": true}, now.Add(-90*time.Minute))
	standInLine(t, store, "!00000000000000000006-1-1", "a desktop CI job past the line's limit", map[string]bool{"is_low_priority": true}, now.Add(-3*time.Hour))

	// Act
	runs, err := MachineLongTurnWaits(now)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	who := make([]string, 0, len(runs))
	for _, run := range runs {
		who = append(who, run.Who)
	}
	if !slices.Equal(who, []string{"a goblin's own turn an hour old"}) {
		t.Errorf("MachineLongTurnWaits = %q; want only the goblin's own turn an hour old", who)
	}
}

// standInLine writes a place named place in the line of store, held by this
// live process since joined, with a card saying who waits there and the kind
// of run it is, as is_test_step or is_low_priority.
func standInLine(t *testing.T, store, place, who string, kind map[string]bool, joined time.Time) {
	t.Helper()
	line := filepath.Join(store, "line")
	if err := os.MkdirAll(line, 0o755); err != nil {
		t.Fatal(err)
	}
	self := t.TempDir()
	owner, err := lock.AcquireExclusiveNamedStrict(self, "place")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.ReleaseExclusiveNamed(self, "place") })
	owner.Acquired = joined.UTC()
	data, err := json.Marshal(owner)
	if err != nil {
		t.Fatal(err)
	}
	says := map[string]any{"who": who}
	for field, is := range kind {
		says[field] = is
	}
	card, err := json.Marshal(says)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(line, place), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(line, place+".run"), card, 0o644); err != nil {
		t.Fatal(err)
	}
}

// longTurnWaitWakes returns the queued wakes of a goblin's turn waiting long.
func longTurnWaitWakes(t *testing.T, h home.Home) []wake.Record {
	t.Helper()
	var waits []wake.Record
	for _, record := range fleetWakeRecords(t, h, "idle") {
		if record.Key == "heavy-line-turn-wait" {
			waits = append(waits, record)
		}
	}
	return waits
}
