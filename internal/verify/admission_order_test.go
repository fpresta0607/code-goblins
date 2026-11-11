package verify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/lock"
)

// The line is ordered by the names of its places and nothing else, so an older
// build agrees with a newer one on it. Each tier here joined before the one in
// front of it, so only the tier can put it behind.
func TestEveryTierSortsBehindTheOneInFrontOfItWhenItJoinedFirst(t *testing.T) {
	// Arrange
	fromTheFront := []Admission{
		{IsTestStep: true, Priority: "production-defect"},
		{IsTestStep: true},
		{IsTestStep: true, Priority: "1"},
		{IsTestStep: true, Priority: "2"},
		{IsTestStep: true, Priority: "3"},
		{IsTestStep: true, Priority: "ops-root"},
		{Priority: "production-defect"},
		{},
		{Priority: "1"},
		{Priority: "2"},
		{Priority: "3"},
		{Priority: "ops-root"},
		{IsLowPriority: true},
	}
	places := []string{overduePlace + fmt.Sprintf("%020d-1-1", len(fromTheFront)+1)}

	// Act
	for at, run := range fromTheFront {
		places = append(places, run.tier()+fmt.Sprintf("%020d-1-1", len(fromTheFront)-at))
	}

	// Assert
	if !slices.IsSorted(places) {
		t.Errorf("the places sort as %q; want them in the order of the line", places)
	}
}

// A run with no task keeps the place every run had before tiers: a name of
// digits, which an older build reads as its own.
func TestARunWithNoRankedPriorityHasNoTier(t *testing.T) {
	for _, priority := range []string{"", "0", "a strategy part, then the Queued rows"} {
		t.Run(priority, func(t *testing.T) {
			// Act
			tier := Admission{Priority: priority}.tier()

			// Assert
			if tier != "" {
				t.Errorf("priority %q has the tier %q; want none", priority, tier)
			}
		})
	}
}

// joined joins run to the line and waits until it says that it waits, so
// every later run asks after it.
func joined(t *testing.T, run Admission, what string) <-chan Turn {
	t.Helper()
	var says said
	run.Waiting = says.waiting
	taken := take(t, run)
	says.waits(t, taken, what)
	return taken
}

// lineShows waits until the runs waiting in dir pass ok, for at most a minute.
func lineShows(t *testing.T, dir, what string, ok func(waiting []Standing) bool) {
	t.Helper()
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(5 * time.Millisecond) {
		_, waiting, err := Line(dir)
		if err == nil && ok(waiting) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("after a minute, not yet: %s; the line is %+v, %v", what, waiting, err)
		}
	}
}

// The line's two orderings, each shown by a run passing one that asked first.
func TestARunPassesALowerOneThatAskedFirst(t *testing.T) {
	for name, test := range map[string]struct{ earlier, later Admission }{
		"a Test step passes a goblin's own turn":                {Admission{}, Admission{IsTestStep: true}},
		"a Test step passes a turn of the same task priority":   {Admission{Priority: "1"}, Admission{IsTestStep: true, Priority: "1"}},
		"the lowest Test step passes the highest own turn":      {Admission{Priority: "production-defect"}, Admission{IsTestStep: true, Priority: "ops-root"}},
		"a production defect passes priority 0":                 {Admission{Priority: "0"}, Admission{Priority: "production-defect"}},
		"priority 0 passes priority 1":                          {Admission{Priority: "1"}, Admission{Priority: "0"}},
		"priority 1 passes priority 2":                          {Admission{Priority: "2"}, Admission{Priority: "1"}},
		"priority 2 passes priority 3":                          {Admission{Priority: "3"}, Admission{Priority: "2"}},
		"priority 3 passes ops-root":                            {Admission{Priority: "ops-root"}, Admission{Priority: "3"}},
		"among Test steps the higher priority passes":           {Admission{IsTestStep: true, Priority: "ops-root"}, Admission{IsTestStep: true, Priority: "0"}},
		"a run with no task passes a lower-priority task's run": {Admission{Priority: "2"}, Admission{}},
		"ops-root passes a run at the lowest priority":          {Admission{IsLowPriority: true}, Admission{Priority: "ops-root"}},
		"a low-priority run's task priority does not lift it":   {Admission{IsLowPriority: true, Priority: "production-defect"}, Admission{Priority: "ops-root"}},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			a := admission(t)
			holder := within(t, take(t, a), "the holder")
			earlier, later := a, a
			earlier.IsTestStep, earlier.Priority, earlier.IsLowPriority = test.earlier.IsTestStep, test.earlier.Priority, test.earlier.IsLowPriority
			later.IsTestStep, later.Priority, later.IsLowPriority = test.later.IsTestStep, test.later.Priority, test.later.IsLowPriority
			passed := joined(t, earlier, "the run that asked first")
			passing := joined(t, later, "the run that asked second")

			// Act
			holder.Release()

			// Assert
			turn := within(t, passing, "the run that asked second")
			notYet(t, passed, "the run that asked first, while the run that passed it held the turn,")
			turn.Release()
			within(t, passed, "the run that asked first").Release()
		})
	}
}

// Runs of one tier keep the order they asked in, and a run with no task keeps
// its place beside a run of the top ordinary priority, either way round.
func TestRunsOfOneTierKeepTheOrderTheyAsked(t *testing.T) {
	for name, test := range map[string]struct{ earlier, later Admission }{
		"two runs with no task":                       {Admission{}, Admission{}},
		"two runs of priority 0":                      {Admission{Priority: "0"}, Admission{Priority: "0"}},
		"two runs of priority 2":                      {Admission{Priority: "2"}, Admission{Priority: "2"}},
		"two runs of ops-root":                        {Admission{Priority: "ops-root"}, Admission{Priority: "ops-root"}},
		"two production defects":                      {Admission{Priority: "production-defect"}, Admission{Priority: "production-defect"}},
		"two Test steps":                              {Admission{IsTestStep: true}, Admission{IsTestStep: true}},
		"two Test steps of priority 1":                {Admission{IsTestStep: true, Priority: "1"}, Admission{IsTestStep: true, Priority: "1"}},
		"a run with no task, then priority 0":         {Admission{}, Admission{Priority: "0"}},
		"priority 0, then a run with no task":         {Admission{Priority: "0"}, Admission{}},
		"a priority the line does not know, then one": {Admission{Priority: "soon"}, Admission{}},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			a := admission(t)
			holder := within(t, take(t, a), "the holder")
			earlier, later := a, a
			earlier.IsTestStep, earlier.Priority = test.earlier.IsTestStep, test.earlier.Priority
			later.IsTestStep, later.Priority = test.later.IsTestStep, test.later.Priority
			first := joined(t, earlier, "the run that asked first")
			second := joined(t, later, "the run that asked second")

			// Act
			holder.Release()

			// Assert
			turn := within(t, first, "the run that asked first")
			notYet(t, second, "the run that asked second, while the first held the turn,")
			turn.Release()
			within(t, second, "the run that asked second").Release()
		})
	}
}

// A goblin's own turn that has started is its own: a Test step of the top
// priority waits for it to end, and the holder keeps its slot and its card.
func TestATestStepWaitsForATurnThatHasStarted(t *testing.T) {
	// Arrange
	a := admission(t)
	a.Who = "a goblin's own turn"
	holder := within(t, take(t, a), "the goblin's own turn")
	step := a
	step.Who, step.IsTestStep, step.Priority = "a gate's Test step", true, "production-defect"

	// Act
	waiting := joined(t, step, "the Test step")

	// Assert
	notYet(t, waiting, "the Test step, while the goblin's own turn ran,")
	holding, _, err := Line(a.Dir)
	if err != nil || len(holding) != 1 || holding[0].Who != a.Who {
		t.Fatalf("Line holding = %+v, %v; want the goblin's own turn holding still", holding, err)
	}
	if err := holder.Release(); err != nil {
		t.Fatal(err)
	}
	within(t, waiting, "the Test step, once the turn ended").Release()
}

// A run of any tier that has waited the limit goes ahead of every run that has
// not, the top of the line included, so no tier can starve it.
func TestARunPastTheLimitGoesAheadOfEveryTier(t *testing.T) {
	for name, tier := range map[string]Admission{
		"a run at the lowest priority": {IsLowPriority: true},
		"an ops-root task's run":       {Priority: "ops-root"},
		"a run with no task":           {},
		"a Test step of low priority":  {IsTestStep: true, Priority: "3"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			a := admission(t)
			holder := within(t, take(t, a), "the holder")
			overdue, urgent := a, a
			overdue.Who, overdue.IsLowPriority, overdue.IsTestStep, overdue.Priority, overdue.overdueAfter = "the run past the limit", tier.IsLowPriority, tier.IsTestStep, tier.Priority, 50*time.Millisecond
			urgent.Who, urgent.IsTestStep, urgent.Priority = "the top of the line", true, "production-defect"
			waited := joined(t, overdue, "the run past the limit")
			passing := joined(t, urgent, "the top of the line")
			lineShows(t, a.Dir, "the run past the limit stands first", func(waiting []Standing) bool {
				return len(waiting) == 2 && waiting[0].Who == overdue.Who
			})

			// Act
			holder.Release()

			// Assert
			turn := within(t, waited, "the run past the limit")
			notYet(t, passing, "the top of the line, while the run past the limit held the turn,")
			turn.Release()
			within(t, passing, "the top of the line").Release()
		})
	}
}

// Runs past the limit go in the order they asked, whatever their tiers were.
func TestRunsPastTheLimitKeepTheOrderTheyAsked(t *testing.T) {
	// Arrange
	a := admission(t)
	holder := within(t, take(t, a), "the holder")
	lower, higher := a, a
	lower.Who, lower.IsLowPriority, lower.overdueAfter = "the lower run that asked first", true, 50*time.Millisecond
	higher.Who, higher.IsTestStep, higher.overdueAfter = "the higher run that asked second", true, 50*time.Millisecond
	first := joined(t, lower, "the lower run")
	second := joined(t, higher, "the higher run")
	lineShows(t, a.Dir, "both runs past the limit, the elder first", func(waiting []Standing) bool {
		return len(waiting) == 2 && waiting[0].Who == lower.Who && strings.HasPrefix(waiting[1].place, overduePlace)
	})

	// Act
	holder.Release()

	// Assert
	turn := within(t, first, "the lower run that asked first")
	notYet(t, second, "the higher run, while the elder held the turn,")
	turn.Release()
	within(t, second, "the higher run").Release()
}

// A run that moves up the line leaves its old place behind, so nothing stands
// in line for it twice, and still says how long it has waited.
func TestARunThatMovesUpStandsOnceAndKeepsTheTimeItJoined(t *testing.T) {
	// Arrange
	a := admission(t)
	holder := within(t, take(t, a), "the holder")
	defer holder.Release()
	overdue := a
	overdue.overdueAfter = 50 * time.Millisecond

	// Act
	joined(t, overdue, "the run past the limit")
	lineShows(t, a.Dir, "the run stands once, past the limit", func(waiting []Standing) bool {
		return len(waiting) == 1 && strings.HasPrefix(waiting[0].place, overduePlace)
	})

	// Assert
	_, waiting, err := Line(a.Dir)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(a.Dir, "line"))
	if err != nil || len(entries) != 2 {
		t.Fatalf("the line's folder holds %v, %v; want one lock and its card", entries, err)
	}
	moved, err := lock.ReadNamedStrict(filepath.Join(a.Dir, "line"), waiting[0].place)
	if err != nil || !waiting[0].Since.Before(moved.Acquired) {
		t.Errorf("the run says it joined at %v, and its new place was taken at %+v, %v; want the moment it first joined", waiting[0].Since, moved, err)
	}
}

// A place that cannot be moved leaves the run where it stands, with nothing
// left at the front to hold every other run back.
func TestARunThatCannotMoveUpKeepsItsPlaceAndLeavesNoneInFront(t *testing.T) {
	const old, front = "00000000000000000001-10-1", overduePlace + "00000000000000000001-10-1"
	for name, block := range map[string]func(t *testing.T, line string){
		"another holds the front place": func(t *testing.T, line string) {
			held, err := lock.AcquireExclusiveNamedStrict(line, front)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { lock.ReleaseExclusiveOwned(context.Background(), line, front, *held) })
		},
		"its card cannot be written": func(t *testing.T, line string) {
			if err := os.Mkdir(filepath.Join(line, front+".run"), 0o755); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			a := admission(t)
			line := filepath.Join(a.Dir, "line")
			if err := os.MkdirAll(line, 0o755); err != nil {
				t.Fatal(err)
			}
			owner, err := lock.AcquireExclusiveNamedStrict(line, old)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { a.remove(line, old, owner) })
			if err := a.leave(line, old, card{Who: a.Who}); err != nil {
				t.Fatal(err)
			}
			block(t, line)

			// Act
			moved, err := a.moveToFront(line, front, old, owner, card{Who: a.Who})

			// Assert
			if err == nil || moved != nil {
				t.Fatalf("moveToFront = %+v, %v; want an error and no new place", moved, err)
			}
			if current, err := lock.ReadNamedStrict(line, old); err != nil || *current != *owner {
				t.Errorf("the old place reads %+v, %v; want it still the run's", current, err)
			}
			if name == "its card cannot be written" {
				if _, err := lock.ReadNamedStrict(line, front); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("the front place reads %v; want it gone, so it holds no run back", err)
				}
			}
		})
	}
}

// Line says why each run stands behind the one before it, in the words goblins
// gate turns prints, and says nothing of the first.
func TestLineSaysWhyARunStandsBehindTheOneBeforeIt(t *testing.T) {
	type place struct{ name, priority string }
	limit := fmt.Sprintf("it has waited past the line's limit of %s", overdueLimit)
	for name, test := range map[string]struct {
		front, back place
		want        string
	}{
		"a Test step":             {place{name: testStepPlace}, place{}, "a gate's Test step goes ahead of a goblin's own turn"},
		"a Test step of any task": {place{testStepPlace + "d-", "ops-root"}, place{"-", "production-defect"}, "a gate's Test step goes ahead of a goblin's own turn"},
		"a higher priority":       {place{"", "0"}, place{"c-", "3"}, "task priority 0 goes ahead of task priority 3"},
		"a production defect":     {place{"-", "production-defect"}, place{"", "0"}, "task priority production-defect goes ahead of task priority 0"},
		"no task":                 {place{}, place{"d-", "ops-root"}, "no task priority goes ahead of task priority ops-root"},
		"Test steps by priority":  {place{testStepPlace, "0"}, place{testStepPlace + "a-", "1"}, "task priority 0 goes ahead of task priority 1"},
		"the lowest priority":     {place{"d-", "ops-root"}, place{name: lowPriorityPlace}, "any other run goes ahead of one at the lowest priority"},
		"past the limit":          {place{overduePlace, "ops-root"}, place{testStepPlace, "0"}, limit},
		"a tie":                   {place{"", "0"}, place{}, "it asked first"},
		"a tie of Test steps":     {place{testStepPlace + "a-", "1"}, place{testStepPlace + "a-", "1"}, "it asked first"},
		"a tie past the limit":    {place{overduePlace, "ops-root"}, place{overduePlace, "0"}, "it asked first"},
		"a tie at the lowest":     {place{lowPriorityPlace, "ops-root"}, place{lowPriorityPlace, "0"}, "it asked first"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			dir := t.TempDir()
			line := filepath.Join(dir, "line")
			if err := os.MkdirAll(line, 0o755); err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			for at, run := range []place{test.front, test.back} {
				record, err := json.Marshal(lock.Info{PID: 4242, OwnerPID: 4242, Hostname: "another-machine", Start: now.Add(-3 * time.Hour), Acquired: now})
				if err != nil {
					t.Fatal(err)
				}
				says, err := json.Marshal(card{Who: "a run", Priority: run.priority})
				if err != nil {
					t.Fatal(err)
				}
				name := run.name + fmt.Sprintf("%020d-10-1", at)
				for file, data := range map[string][]byte{name: record, name + ".run": says} {
					if err := os.WriteFile(filepath.Join(line, file), data, 0o644); err != nil {
						t.Fatal(err)
					}
				}
			}

			// Act
			_, waiting, err := Line(dir)

			// Assert
			if err != nil || len(waiting) != 2 || waiting[0].Behind != "" || waiting[1].Behind != test.want {
				t.Errorf("Line waiting = %+v, %v; want nothing said of the first and %q of the second", waiting, err, test.want)
			}
		})
	}
}
