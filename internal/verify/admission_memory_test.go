package verify

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/lock"
)

// twoSlots is a line of two slots on a machine with free memory, which the
// test may change.
func twoSlots(t *testing.T, free *atomic.Uint64) Admission {
	t.Helper()
	a := admissionWithCapacity(t, 2)
	a.Floor = 0
	a.Available = func() (uint64, error) { return free.Load(), nil }
	return a
}

// A second run takes the other slot when what the holder may still use, its
// own estimate and the desktop reserve fit in free memory.
func TestASecondRunTakesTheOtherSlotWhenItsMemoryFits(t *testing.T) {
	// Arrange
	var free atomic.Uint64
	free.Store(8 * gigabyte)
	holding := twoSlots(t, &free)
	holding.Estimate = 2 * gigabyte
	holder := within(t, take(t, holding), "the holder")
	defer holder.Release()
	holder.Measure(3 * gigabyte / 2)
	second := holding
	second.Estimate = 0

	// Act
	turn := within(t, take(t, second), "a run needing 0.5 + 2.5 + 2 GiB of 8")

	// Assert
	if err := turn.Release(); err != nil {
		t.Fatal(err)
	}
}

// A run waits beside a holder that has not reached its estimate, and takes
// the slot once the holder's use shows it has.
func TestASecondRunWaitsBesideAHolderShortOfItsEstimate(t *testing.T) {
	// Arrange
	var free atomic.Uint64
	free.Store(8 * gigabyte)
	holding := twoSlots(t, &free)
	holding.Estimate = 6 * gigabyte
	holder := within(t, take(t, holding), "the holder")
	defer holder.Release()
	second := holding
	second.Estimate = 0
	var says said
	second.Waiting = says.waiting
	taken := take(t, second)

	// Act
	why := says.waits(t, taken, "a run needing 6 + 2.5 + 2 GiB of 8")
	holder.Measure(6 * gigabyte)

	// Assert
	turn := within(t, taken, "a run needing 0 + 2.5 + 2 GiB of 8")
	if err := turn.Release(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(why, "8.0 GB of memory is available and the floor is 10.5 GB") {
		t.Errorf("the run said it waits for %q; want the weighed floor named", why)
	}
}

// A run that fits the memory free goes past a run ahead of it that does not,
// which takes its turn once the memory is there: on 2026-10-29 a waiter
// expecting 8 GB with 7.1 GB free held eight runs that fit for 28 minutes
// while the turn sat free (#1743).
func TestARunThatFitsGoesPastARunAheadThatDoesNot(t *testing.T) {
	// Arrange
	var free atomic.Uint64
	free.Store(8 * gigabyte)
	holding := twoSlots(t, &free)
	holding.Estimate = gigabyte
	holder := within(t, take(t, holding), "the holder")
	defer holder.Release()
	holder.Measure(gigabyte)
	large := holding
	large.Estimate = 7 * gigabyte
	var largeSays said
	large.Waiting = largeSays.waiting
	takenLarge := take(t, large)
	largeSays.waits(t, takenLarge, "a run needing 0 + 7 + 2 GiB of 8")
	small := holding
	small.Estimate = 0

	// Act
	turn := within(t, take(t, small), "a run needing 0 + 2.5 + 2 GiB of 8, behind one that does not fit")

	// Assert
	notYet(t, takenLarge, "the run that does not fit")
	if err := turn.Release(); err != nil {
		t.Fatal(err)
	}
	free.Store(9 * gigabyte)
	if err := within(t, takenLarge, "the run that did not fit, once 9 GiB is free").Release(); err != nil {
		t.Fatal(err)
	}
}

// A run that has waited the line's limit is never passed, even by a run that
// fits when it does not, so a run expected to use more than is free waits for
// its turn no longer than that limit.
func TestAnOverdueRunIsNeverPassedByARunThatFits(t *testing.T) {
	// Arrange
	var free atomic.Uint64
	free.Store(8 * gigabyte)
	holding := twoSlots(t, &free)
	holding.Estimate = gigabyte
	holder := within(t, take(t, holding), "the holder")
	defer holder.Release()
	holder.Measure(gigabyte)
	large := holding
	large.Estimate, large.overdueAfter = 7*gigabyte, 50*time.Millisecond
	takenLarge := take(t, large)
	for deadline := time.Now().Add(time.Minute); !hasOverduePlace(t, holding.Dir); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the large run never moved to the overdue place")
		}
	}
	small := holding
	small.Estimate = 0
	takenSmall := take(t, small)

	// Act
	notYet(t, takenSmall, "a run behind an overdue one")
	free.Store(9 * gigabyte)

	// Assert
	if err := within(t, takenLarge, "the overdue run, once 9 GiB is free").Release(); err != nil {
		t.Fatal(err)
	}
	if err := within(t, takenSmall, "the run behind it, once it went").Release(); err != nil {
		t.Fatal(err)
	}
}

// A run that does not fit is passed by a run that fits only during its first
// 20 minutes in line: its floor wait ends after an hour, and a run passed for
// all of that hour would be refused before its turn ever came.
func TestARunThatDoesNotFitIsPassedOnlyInItsFirstTwentyMinutes(t *testing.T) {
	cases := []struct {
		name     string
		age      time.Duration
		isPassed bool
	}{
		{name: "19 minutes in line", age: 19 * time.Minute, isPassed: true},
		{name: "21 minutes in line", age: 21 * time.Minute, isPassed: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			var free atomic.Uint64
			free.Store(8 * gigabyte)
			holding := twoSlots(t, &free)
			holding.Estimate = gigabyte
			holder := within(t, take(t, holding), "the holder")
			defer holder.Release()
			holder.Measure(gigabyte)
			line := filepath.Join(holding.Dir, "line")
			place := fmt.Sprintf("%020d-%d-%d", time.Now().Add(-testCase.age).UTC().UnixNano(), os.Getpid(), 1)
			owner, err := lock.AcquireExclusiveNamedStrict(line, place)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := holding.remove(line, place, owner); err != nil {
					t.Error(err)
				}
			})
			if err := holding.leave(line, place, card{Who: "a run needing 0 + 7 + 2 GiB of 8", EstimateBytes: 7 * gigabyte}); err != nil {
				t.Fatal(err)
			}
			small := holding
			small.Estimate = 0

			// Act
			takenSmall := take(t, small)

			// Assert
			if !testCase.isPassed {
				notYet(t, takenSmall, "a run that fits, behind one that has stood 21 minutes in line")
				return
			}
			if err := within(t, takenSmall, "a run that fits, behind one that has stood 19 minutes in line").Release(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// A run that was passed starts its floor wait only once nothing can pass it,
// so runs that passed it and hold the slots longer than its floor wait has
// left from when it first found memory short do not use that wait up: it
// takes its turn once they go.
func TestARunThatWasPassedWaitsForMemoryFromWhenNothingCanPassIt(t *testing.T) {
	// Arrange
	var free atomic.Uint64
	free.Store(8 * gigabyte)
	holding := twoSlots(t, &free)
	large := holding
	large.Estimate, large.Limit, large.passAfter = 7*gigabyte, 10*time.Second, 40*time.Second
	var largeSays said
	large.Waiting = largeSays.waiting
	takenLarge := take(t, large)
	largeSays.waits(t, takenLarge, "a run needing 0 + 7 + 2 GiB of 8")
	firstShort := time.Now()
	small := holding
	small.Estimate = 0
	first := within(t, take(t, small), "the first run that fits, past the one that does not")
	second := within(t, take(t, small), "the second run that fits, past the one that does not")

	// Act
	time.Sleep(time.Until(firstShort.Add(large.Limit + time.Second))) // stall-accepted: the passed run's floor wait has 39 s more, so a stall only delays the release it waits behind.
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	if err := second.Release(); err != nil {
		t.Fatal(err)
	}
	free.Store(9 * gigabyte)

	// Assert
	if err := within(t, takenLarge, "the passed run, once the runs past it went").Release(); err != nil {
		t.Fatal(err)
	}
}

// A run that nothing passes waits for memory from when it first found memory
// short, as it did before runs that fit could pass it.
func TestARunThatNothingPassesWaitsForMemoryFromWhenItFirstFoundItShort(t *testing.T) {
	// Arrange
	var free atomic.Uint64
	free.Store(8 * gigabyte)
	large := twoSlots(t, &free)
	large.Estimate, large.Limit = 7*gigabyte, time.Second
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	// Act
	turn, err := large.Wait(ctx)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "memory floor wait expired") {
		t.Fatalf("the run that nothing passes = %+v, %v; want its floor wait expired after its limit, not its pass limit later", turn, err)
	}
	if turn.Waited < large.Limit {
		t.Errorf("the run waited %s; want at least its limit of %s", turn.Waited, large.Limit)
	}
}

// The runs a run finds holding the slots when it first finds memory short at
// the head took their turns before it, so they did not pass it, and it waits
// for memory from that first look, as it did before runs could pass it.
func TestARunShortBesideTheHolderItFirstFoundWaitsForMemoryFromThatLook(t *testing.T) {
	// Arrange
	var free atomic.Uint64
	free.Store(8 * gigabyte)
	holding := twoSlots(t, &free)
	holding.Estimate = 2 * gigabyte
	holder := within(t, take(t, holding), "the holder")
	defer holder.Release()
	holder.Measure(2 * gigabyte)
	large := holding
	large.Estimate, large.Limit, large.passAfter = 7*gigabyte, time.Second, time.Hour
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	// Act
	turn, err := large.Wait(ctx)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "memory floor wait expired") {
		t.Fatalf("the run beside the holder it first found = %+v, %v; want its floor wait expired after its limit, not its pass limit later", turn, err)
	}
	if turn.Waited < large.Limit {
		t.Errorf("the run waited %s; want at least its limit of %s", turn.Waited, large.Limit)
	}
}

// hasOverduePlace says a run in the line kept in dir stands at the overdue
// place.
func hasOverduePlace(t *testing.T, dir string) bool {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, "line"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), overduePlace) && !strings.HasSuffix(entry.Name(), ".run") {
			return true
		}
	}
	return false
}

// What a holder may still use is its estimate less what it commits now,
// never below nothing, and a holder that named no estimate, as an older
// build's, is weighed at the default.
func TestHoldersRemainingWeighsEachHoldersEstimate(t *testing.T) {
	for name, row := range map[string]struct {
		says card
		want uint64
	}{
		"an estimate unreached":   {says: card{Who: "a", EstimateBytes: 3 * gigabyte, UsedBytes: gigabyte}, want: 2 * gigabyte},
		"an estimate passed":      {says: card{Who: "a", EstimateBytes: gigabyte, UsedBytes: 2 * gigabyte}, want: 0},
		"no estimate on its card": {says: card{Who: "a", UsedBytes: gigabyte / 2}, want: defaultEstimate - gigabyte/2},
		"nothing measured":        {says: card{Who: "a", EstimateBytes: 2 * gigabyte}, want: 2 * gigabyte},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			dir := t.TempDir()
			if _, err := lock.AcquireExclusiveNamedStrict(dir, "slot-1"); err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(row.says)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "slot-1.run"), data, 0o600); err != nil {
				t.Fatal(err)
			}

			// Act
			remaining, err := holdersRemaining(dir)

			// Assert
			if err != nil || remaining != row.want {
				t.Errorf("holdersRemaining = %d, %v; want %d", remaining, err, row.want)
			}
		})
	}
}

// The weighed floor keeps the desktop reserve the run's monitor keeps: the
// store's reserve.json, which a caller may raise and never lower, or the
// default without one. A broken reserve.json is refused, as the monitor
// refuses it. Each row weighs a run expected to use 4 GiB on an empty line.
func TestTheWeighedFloorKeepsTheMonitorsDesktopReserve(t *testing.T) {
	for name, row := range map[string]struct {
		setting   string
		requested uint64
		want      uint64
	}{
		"no reserve.json":            {want: 6 * gigabyte},
		"a store reserve raised":     {setting: `{"physical_gib": 3, "commit_gib": 1}`, want: 7 * gigabyte},
		"a store reserve lowered":    {setting: `{"physical_gib": 1, "commit_gib": 1}`, want: 5 * gigabyte},
		"a caller raising the store": {setting: `{"physical_gib": 1, "commit_gib": 1}`, requested: 3 * gigabyte, want: 7 * gigabyte},
		"a broken reserve.json":      {setting: `{"physical_gib": 9, "commit_gib": 1}`},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			dir := admissionWithCapacity(t, 2).Dir
			if row.setting != "" {
				if err := os.WriteFile(filepath.Join(dir, "reserve.json"), []byte(row.setting), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			// Act
			floor, err := HeadFloor(dir, 4*gigabyte, row.requested)

			// Assert
			if row.want == 0 {
				if err == nil || !strings.Contains(err.Error(), "reserve.json") {
					t.Errorf("HeadFloor = %d, %v; want reserve.json refused", floor, err)
				}
				return
			}
			if err != nil || floor != row.want {
				t.Errorf("HeadFloor = %d, %v; want %d", floor, err, row.want)
			}
		})
	}
}

// A holder that was ended leaves its record and its card until the next run
// takes its slot, and what it might still have used holds no run back.
func TestADeadHoldersEstimateHoldsNoRunBack(t *testing.T) {
	// Arrange
	a := admissionWithCapacity(t, 2)
	holder := exec.Command(os.Args[0])
	holder.Env = append(os.Environ(), "VERIFY_TEST_HOLD_DIR="+a.Dir)
	stdin, err := holder.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	stdout, err := holder.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || strings.TrimSpace(line) != "holding" {
		holder.Process.Kill()
		holder.Wait()
		t.Fatalf("holder: %q, %v", line, err)
	}
	if err := holder.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	holder.Wait()
	if says := readCard(filepath.Join(a.Dir, "slot-1")); says.EstimateBytes != defaultEstimate {
		t.Fatalf("the ended holder's card says %+v; want its estimate left on it", says)
	}

	// Act
	remaining, err := holdersRemaining(a.Dir)

	// Assert
	if err != nil || remaining != 0 {
		t.Errorf("holdersRemaining = %d, %v; want nothing for a holder that was ended", remaining, err)
	}
}

// The weighed floor never asks less than one slot's 4 GiB, however small the
// run and empty the line.
func TestTheWeighedFloorNeverAsksLessThanOneSlots(t *testing.T) {
	// Arrange
	dir := admissionWithCapacity(t, 2).Dir

	// Act
	floor, err := HeadFloor(dir, 512<<20, 0)

	// Assert
	if err != nil || floor != 4*gigabyte {
		t.Errorf("HeadFloor = %d, %v; want 4 GiB for a run of 512 MiB and the 2 GiB reserve", floor, err)
	}
}

// A holder's estimate and use go on its card, which the line reads, and
// what it says it is doing keeps them. A use within 64 MiB of the card's is
// not written again.
func TestMeasureWritesTheHoldersUseBesideItsTurn(t *testing.T) {
	// Arrange
	a := admissionWithCapacity(t, 2)
	a.Estimate = 3 * gigabyte
	turn := within(t, take(t, a), "the holder")
	defer turn.Release()

	// Act
	turn.Measure(gigabyte)
	turn.Measure(gigabyte + 32<<20)
	turn.Say("running its tests")

	// Assert
	holding, _, err := Line(a.Dir)
	if err != nil || len(holding) != 1 {
		t.Fatalf("Line = %+v, %v; want the one holder", holding, err)
	}
	if holding[0].Estimate != 3*gigabyte || holding[0].Used != gigabyte || holding[0].Now != "running its tests" {
		t.Errorf("the holder's card says estimate %d, used %d, now %q; want 3 GiB, 1 GiB and what it said", holding[0].Estimate, holding[0].Used, holding[0].Now)
	}
}

// A run that waits says its estimate on its card, for whoever reads the line.
func TestAWaitingRunSaysItsEstimate(t *testing.T) {
	// Arrange
	var free atomic.Uint64
	free.Store(gigabyte)
	a := twoSlots(t, &free)
	a.Estimate = 7 * gigabyte
	var says said
	a.Waiting = says.waiting
	taken := take(t, a)
	says.waits(t, taken, "a run short of memory")

	// Act
	_, waiting, err := Line(a.Dir)

	// Assert
	if err != nil || len(waiting) != 1 || waiting[0].Estimate != 7*gigabyte {
		t.Errorf("Line's waiting = %+v, %v; want the run with its 7 GiB estimate", waiting, err)
	}
}
