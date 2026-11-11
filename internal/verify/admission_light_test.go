package verify

import (
	"context"
	"strings"
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

// A light run takes its turn beside the slots once free memory covers its own
// estimate and the desktop reserve, while two heavy runs hold both slots, and
// waits while memory does not.
func TestALightRunTakesItsTurnBesideTwoHeavyRunsWhenMemoryCoversItsEstimate(t *testing.T) {
	for _, row := range []struct {
		name        string
		available   uint64
		isAdmitted  bool
		description string
	}{
		{"memory over its estimate", 3*gigabyte + 1, true, "1 GiB estimate and 2 GiB reserve covered"},
		{"memory exactly its estimate", 3 * gigabyte, true, "1 GiB estimate and 2 GiB reserve covered"},
		{"memory under its estimate", 3*gigabyte - 1, false, "one byte short of its estimate and the reserve"},
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

// A command started inside a light turn, as cfo gate test is behind a light
// cfo gate turn, runs inside it rather than waiting for a slot the heavy
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
