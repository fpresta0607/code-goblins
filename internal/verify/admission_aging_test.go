package verify

import (
	"strings"
	"testing"
	"time"
)

// A goblin's own turn that has waited its aging limit stands ahead of every
// gate Test step that asks after it, whatever the priority of either (#2416).
func TestAGoblinTurnPastItsAgingLimitGoesAheadOfATestStepThatAskedAfterIt(t *testing.T) {
	for name, priority := range map[string]string{
		"a run with no task":        "",
		"an ops-root task's run":    "ops-root",
		"a production defect's run": "production-defect",
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			a := admission(t)
			holder := within(t, take(t, a), "the holder")
			own, step := a, a
			own.Who, own.Priority, own.agingAfter = "the goblin's own turn", priority, 50*time.Millisecond
			step.Who, step.IsTestStep, step.Priority = "a gate's Test step", true, "production-defect"
			waited := joined(t, own, "the goblin's own turn")
			lineShows(t, a.Dir, "the goblin's own turn has aged", func(waiting []Standing) bool {
				return len(waiting) == 1 && strings.HasPrefix(waiting[0].place, agedPlace)
			})
			passing := joined(t, step, "the Test step")
			_, waiting, err := Line(a.Dir)
			if err != nil || len(waiting) != 2 || !waiting[1].IsTestStep || waiting[1].Behind != "a goblin's own turn that has waited 20m0s goes ahead of what asked after it" {
				t.Fatalf("the line is %+v, %v; want the Test step behind the aged goblin's turn, saying why", waiting, err)
			}

			// Act
			holder.Release()

			// Assert
			turn := within(t, waited, "the goblin's own turn")
			notYet(t, passing, "the Test step that asked after it, while the goblin's turn held the turn,")
			turn.Release()
			within(t, passing, "the Test step").Release()
		})
	}
}

// Only a goblin's own turn ages: a gate's Test step already goes ahead of it,
// and a low-priority run waits behind every other run until the line's limit.
func TestATestStepOrALowPriorityRunDoesNotAge(t *testing.T) {
	for name, kind := range map[string]Admission{
		"a gate's Test step":           {IsTestStep: true},
		"a run at the lowest priority": {IsLowPriority: true},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			a := admission(t)
			holder := within(t, take(t, a), "the holder")
			run := a
			run.Who, run.IsTestStep, run.IsLowPriority, run.agingAfter = name, kind.IsTestStep, kind.IsLowPriority, 50*time.Millisecond
			waited := joined(t, run, name)

			// Act
			time.Sleep(200 * time.Millisecond) // the run is meant to stay where it is, so a stall only makes the wait longer

			// Assert
			if _, waiting, err := Line(a.Dir); err != nil || len(waiting) != 1 || strings.HasPrefix(waiting[0].place, agedPlace) {
				t.Errorf("the line is %+v, %v; want %s still at its own place, not aged", waiting, err, name)
			}
			holder.Release()
			within(t, waited, name).Release()
		})
	}
}

// A gate Test step that asked before a goblin's own turn still goes first,
// however long the goblin's turn has waited, and the goblin's turn ages once
// no such step waits, ahead of a Test step that asks after that.
func TestAGoblinTurnPastItsAgingLimitStillWaitsForATestStepThatAskedFirst(t *testing.T) {
	// Arrange
	a := admission(t)
	holder := within(t, take(t, a), "the holder")
	first, own, last := a, a, a
	first.Who, first.IsTestStep = "the Test step that asked first", true
	own.Who, own.agingAfter = "the goblin's own turn", 50*time.Millisecond
	last.Who, last.IsTestStep = "the Test step that asked last", true
	firstStep := joined(t, first, "the Test step that asked first")
	waited := joined(t, own, "the goblin's own turn")
	time.Sleep(200 * time.Millisecond) // the goblin's turn is meant to stay where it is, so a stall only makes the wait longer
	_, waiting, err := Line(a.Dir)
	if err != nil || len(waiting) != 2 || waiting[0].Who != first.Who || strings.HasPrefix(waiting[1].place, agedPlace) {
		t.Fatalf("the line is %+v, %v; want the Test step that asked first ahead of the goblin's turn, which has not aged past it", waiting, err)
	}

	// Act
	holder.Release()
	firstTurn := within(t, firstStep, "the Test step that asked first")
	notYet(t, waited, "the goblin's own turn, while the Test step that asked first held the turn,")
	lineShows(t, a.Dir, "the goblin's own turn has aged", func(waiting []Standing) bool {
		return len(waiting) == 1 && strings.HasPrefix(waiting[0].place, agedPlace)
	})
	lastStep := joined(t, last, "the Test step that asked last")
	firstTurn.Release()

	// Assert
	turn := within(t, waited, "the goblin's own turn")
	notYet(t, lastStep, "the Test step that asked last, while the goblin's turn held the turn,")
	turn.Release()
	within(t, lastStep, "the Test step that asked last").Release()
}

// Gate Test steps that keep arriving, each asking while the one before holds
// the turn, go ahead of a goblin's own turn only until it has aged: on
// 2026-11-12 such a stream kept a goblin's turn waiting over an hour (#2416).
func TestAStreamOfTestStepsLetsAGoblinTurnInOnceItHasAged(t *testing.T) {
	// Arrange
	a := admission(t)
	current := within(t, take(t, a), "the first holder")
	own, step := a, a
	own.Who, own.agingAfter = "the goblin's own turn", 200*time.Millisecond
	step.Who, step.IsTestStep = "a gate's Test step", true
	waited := joined(t, own, "the goblin's own turn")
	var steps []<-chan Turn
	next := 0

	// Act
	for range 200 {
		steps = append(steps, joined(t, step, "a Test step"))
		current.Release()
		select {
		case turn := <-waited:
			// Assert
			turn.Release()
			for ; next < len(steps); next++ {
				within(t, steps[next], "a Test step that asked after the goblin's turn aged").Release()
			}
			return
		case turn := <-steps[next]:
			next++
			current = turn
		case <-time.After(time.Minute):
			t.Fatal("no run took the turn within a minute")
		}
	}
	t.Fatalf("%d Test steps took the turn ahead of the goblin's own turn", next)
}
