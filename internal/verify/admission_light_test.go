package verify

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// lightAdmission is a light run of the store a keeps, expected to use
// estimate, on a machine with available memory free.
func lightAdmission(a Admission, estimate, available uint64) Admission {
	a.IsLight = true
	a.Who = "a light test"
	a.Estimate = estimate
	a.Budget = 10 * time.Minute
	a.Available = func() (uint64, error) { return available, nil }
	return a
}

// A light run takes its turn beside the slots once free memory covers what
// the two heavy runs holding both slots may still use, its own estimate and
// the desktop reserve, and waits while memory does not.
func TestALightRunTakesItsTurnBesideTwoHeavyRunsWhenMemoryCoversItsEstimate(t *testing.T) {
	for _, row := range []struct {
		name        string
		available   uint64
		isAdmitted  bool
		description string
	}{
		{"memory over its floor", 8*gigabyte + 1, true, "the holders' 5 GiB, its 1 GiB estimate and the 2 GiB reserve covered"},
		{"memory exactly its floor", 8 * gigabyte, true, "the holders' 5 GiB, its 1 GiB estimate and the 2 GiB reserve covered"},
		{"memory under its floor", 8*gigabyte - 1, false, "one byte short of the holders' growth, its estimate and the reserve"},
	} {
		t.Run(row.name, func(t *testing.T) {
			// Arrange
			heavy := admissionWithCapacity(t, 2)
			first := within(t, take(t, heavy), "first heavy run")
			defer first.Release()
			second := within(t, take(t, heavy), "second heavy run")
			defer second.Release()
			light := lightAdmission(heavy, gigabyte, row.available)
			light.Limit = 200 * time.Millisecond
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()

			// Act
			turn, err := light.Wait(ctx)
			defer turn.Release()

			// Assert
			if !row.isAdmitted {
				if err == nil || turn.release != nil || !strings.Contains(err.Error(), "memory floor wait expired") {
					t.Fatalf("Wait = %+v, %v; want no turn with %s", turn, err, row.description)
				}
				return
			}
			if err != nil || turn.release == nil {
				t.Fatalf("Wait = %+v, %v; want a light turn beside both held slots with %s", turn, err, row.description)
			}
			holding, waiting, err := Line(heavy.Dir)
			var lightHolders, slotHolders int
			for _, run := range holding {
				if run.IsLight && run.Who == "a light test" && run.Estimate == gigabyte && run.Budget == 10*time.Minute {
					lightHolders++
				} else if !run.IsLight {
					slotHolders++
				}
			}
			if err != nil || lightHolders != 1 || slotHolders != 2 || len(waiting) != 0 {
				t.Fatalf("Line = %+v, %+v, %v; want the light run apart from the two slot holders", holding, waiting, err)
			}
		})
	}
}

// A light run weighs what the heavy runs holding the slots may still use,
// each one's estimate less what it commits now: memory that covers its own
// estimate and the reserve but not the holders' growth leaves it waiting, so
// their monitors never end one of them at the reserve for memory it took, and
// it takes its turn once their growth is covered too.
func TestALightRunWaitsForWhatTheHeavyHoldersMayStillUse(t *testing.T) {
	// Arrange
	heavy := admissionWithCapacity(t, 2)
	heavy.Estimate = 4 * gigabyte
	first := within(t, take(t, heavy), "first heavy run")
	defer first.Release()
	second := within(t, take(t, heavy), "second heavy run")
	defer second.Release()
	first.Measure(gigabyte)
	second.Measure(gigabyte)
	var available atomic.Uint64
	available.Store(gigabyte + 2*gigabyte)
	light := lightAdmission(heavy, gigabyte, 0)
	light.Available = func() (uint64, error) { return available.Load(), nil }
	isWaiting := make(chan string, 1)
	light.Waiting = func(_ time.Duration, why string) {
		select {
		case isWaiting <- why:
		default:
		}
	}

	// Act
	taken := take(t, light)
	var why string
	select {
	case why = <-isWaiting:
	case early := <-taken:
		early.Release()
		t.Fatalf("a light run took %q on 3 GiB free, memory the heavy holders' 6 GiB of growth still needs", early.Token())
	case <-time.After(time.Minute):
		t.Fatal("the light run neither took a turn nor said it waits within a minute")
	}
	notYet(t, taken, "a light run on memory the heavy holders still grow into")
	available.Store(6*gigabyte + gigabyte + 2*gigabyte)
	turn := within(t, taken, "a light run once the holders' 6 GiB of growth, its 1 GiB and the 2 GiB reserve are covered")
	defer turn.Release()

	// Assert
	if !strings.Contains(why, "a light run waits beside the line for memory") || !strings.HasPrefix(turn.Token(), lightPlace) {
		t.Fatalf("waited saying %q and took %q; want a light turn after a wait for memory", why, turn.Token())
	}
}

// Light runs are capped by the memory they may still use together, never by
// how many there are: a second light run is admitted beside the first once
// free memory covers both estimates and the reserve.
func TestALightRunWeighsWhatTheLightRunsBesideItMayStillUse(t *testing.T) {
	for _, row := range []struct {
		name       string
		available  uint64
		isAdmitted bool
	}{
		{"memory covers both", 4 * gigabyte, true},
		{"memory covers only one", 4*gigabyte - 1, false},
	} {
		t.Run(row.name, func(t *testing.T) {
			// Arrange
			a := admissionWithCapacity(t, 2)
			first := within(t, take(t, lightAdmission(a, gigabyte, 16*gigabyte)), "first light run")
			defer first.Release()
			second := lightAdmission(a, gigabyte, row.available)
			second.Limit = 200 * time.Millisecond
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()

			// Act
			turn, err := second.Wait(ctx)
			defer turn.Release()

			// Assert
			if isAdmitted := err == nil && turn.release != nil; isAdmitted != row.isAdmitted {
				t.Fatalf("Wait = %+v, %v; want admitted %v", turn, err, row.isAdmitted)
			}
		})
	}
}

// A light run names the memory it is expected to use and the bound its
// caller ends it at, or takes no turn: unbounded, it would be a heavy run
// that skipped the line.
func TestALightRunWithoutItsEstimateOrBoundTakesNoTurn(t *testing.T) {
	for _, row := range []struct {
		name     string
		estimate uint64
		budget   time.Duration
	}{
		{"no estimate", 0, time.Minute},
		{"no bound", gigabyte, 0},
	} {
		t.Run(row.name, func(t *testing.T) {
			// Arrange
			a := lightAdmission(admission(t), row.estimate, 16*gigabyte)
			a.Budget = row.budget

			// Act
			turn, err := a.Wait(context.Background())
			defer turn.Release()

			// Assert
			if err == nil || turn.release != nil || !strings.Contains(err.Error(), "a light run names the memory") {
				t.Fatalf("Wait = %+v, %v; want a refusal naming the missing estimate or bound", turn, err)
			}
		})
	}
}

// A command started inside a light turn, as goblins gate test is behind a light
// goblins gate turn, runs inside it rather than waiting for a slot the heavy
// runs hold.
func TestARunStartedInsideALightTurnRunsInsideIt(t *testing.T) {
	// Arrange
	heavy := admissionWithCapacity(t, 2)
	first := within(t, take(t, heavy), "first heavy run")
	defer first.Release()
	second := within(t, take(t, heavy), "second heavy run")
	defer second.Release()
	light := within(t, take(t, lightAdmission(heavy, gigabyte, 16*gigabyte)), "light run")
	defer light.Release()
	t.Setenv(TurnVariable, light.Token())
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	// Act
	inner, err := heavy.Wait(ctx)
	defer inner.Release()

	// Assert
	if err != nil || inner.Token() != light.Token() || !strings.HasPrefix(inner.Token(), lightPlace) {
		t.Fatalf("Wait = %+v, %v; want the light turn %q it was started inside", inner, err, light.Token())
	}
}

// A heavy run at the head of a line of two weighs what a light run beside the
// slots may still use, as it weighs a slot holder's.
func TestTheHeadFloorWeighsWhatALightRunMayStillUse(t *testing.T) {
	// Arrange
	a := admissionWithCapacity(t, 2)
	light := within(t, take(t, lightAdmission(a, 3*gigabyte, 16*gigabyte)), "light run")
	defer light.Release()

	// Act
	floor, err := HeadFloor(a.Dir, 0, 0)

	// Assert
	if want := 3*gigabyte + defaultEstimate + 2*gigabyte; err != nil || floor != want {
		t.Fatalf("HeadFloor = %d, %v; want %d, the light run's 3 GiB, the default estimate and the reserve", floor, err, want)
	}
}

// The run at the head of a line of two reads its floor again under the line's
// lock as it takes a slot: a light run admitted after the head last read
// memory, on memory that covered the head's floor, leaves the head waiting
// rather than taking a slot on memory only the light run was weighed against.
func TestAHeadRunWaitsForALightRunAdmittedAfterItReadMemory(t *testing.T) {
	// Arrange
	a := admissionWithCapacity(t, 2)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var light Turn
	defer func() { light.Release() }()
	head := a
	head.Limit = 200 * time.Millisecond
	head.Available = func() (uint64, error) {
		if light.release == nil {
			var err error
			if light, err = lightAdmission(a, 3*gigabyte, 6*gigabyte).Wait(ctx); err != nil {
				t.Errorf("light run between the head's reading and its take: %v", err)
			}
		}
		return 6 * gigabyte, nil
	}

	// Act
	turn, err := head.Wait(ctx)
	defer turn.Release()

	// Assert
	if err == nil || turn.release != nil || !strings.Contains(err.Error(), "memory floor wait expired") {
		t.Fatalf("Wait = %+v, %v; want no slot on 6 GiB free once the light run's 3 GiB, the default estimate and the reserve need 7.5 GiB", turn, err)
	}
	if !strings.HasPrefix(light.Token(), lightPlace) {
		t.Fatalf("light turn %q; want a light run admitted beside the line", light.Token())
	}
}
