package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/verify"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// A heavy line that stands still, a turn free with runs waiting and memory
// enough, wakes the CEO once it has stood still for five minutes of readings,
// not before, once only while it stays still, and again after a reading finds
// it moving and it stands still five minutes more. On 2026-11-11 it stood
// still for over 40 minutes and nobody was told (#2377).
func TestAHeavyLineStandingStillWakesTheCEOOnceAfterFiveMinutes(t *testing.T) {
	// Arrange
	s, h := fleetService(t)
	stall := "1 of 1 turns free and 9 in line, goblins gate test first, with 5.0 GB of memory and commit free against its 4.5 GB floor"
	reading := stall
	s.Options.Dispatch = &Dispatch{
		Memory: func() (Memory, error) {
			return Memory{Available: 6 * gigabyte, CommitAvailable: 6 * gigabyte, Total: 32 * gigabyte}, nil
		},
		LineStall: func(Memory) (string, error) { return reading, nil },
	}
	now := time.Date(2026, 11, 11, 18, 47, 0, 0, time.UTC)
	read := func(readings int) int {
		t.Helper()
		before := len(lineStallWakes(t, h))
		for range readings {
			if err := s.checkFleet(t.Context(), now); err != nil {
				t.Fatal(err)
			}
			now = now.Add(time.Minute)
		}
		return len(lineStallWakes(t, h)) - before
	}

	// Act
	beforeFive := read(5)
	atFive := read(1)
	stillStill := read(10)
	reading = ""
	moving := read(1)
	reading = stall
	again := read(6)

	// Assert
	if beforeFive != 0 || atFive != 1 || stillStill != 0 || moving != 0 || again != 1 {
		t.Fatalf("wakes: %d in the first five minutes, %d at five, %d while still, %d moving, %d after five more; want 0, 1, 0, 0, 1", beforeFive, atFive, stillStill, moving, again)
	}
	detail := lineStallWakes(t, h)[0].Detail
	for _, want := range []string{"heavy_line_stalled: for 5m0s", stall, "goblins gate turns"} {
		if !strings.Contains(detail, want) {
			t.Errorf("wake %q lacks %q", detail, want)
		}
	}
}

// Readings with a gap between them longer than two passes are not readings
// in a row: a line that stood still before the gap and after it wakes the CEO
// only once it has stood still five minutes after the gap, and says so.
func TestAGapBetweenReadingsStartsTheLineStallsFiveMinutesAgain(t *testing.T) {
	// Arrange
	s, h := fleetService(t)
	s.Options.Dispatch = &Dispatch{
		Memory: func() (Memory, error) {
			return Memory{Available: 6 * gigabyte, CommitAvailable: 6 * gigabyte, Total: 32 * gigabyte}, nil
		},
		LineStall: func(Memory) (string, error) { return "1 of 1 turns free and 1 in line", nil },
	}
	now := time.Date(2026, 11, 11, 18, 47, 0, 0, time.UTC)
	read := func(readings int) int {
		t.Helper()
		before := len(lineStallWakes(t, h))
		for range readings {
			if err := s.checkFleet(t.Context(), now); err != nil {
				t.Fatal(err)
			}
			now = now.Add(time.Minute)
		}
		return len(lineStallWakes(t, h)) - before
	}

	// Act
	beforeGap := read(4)
	now = now.Add(time.Hour)
	afterGap := read(5)
	atFive := read(1)

	// Assert
	if beforeGap != 0 || afterGap != 0 || atFive != 1 {
		t.Fatalf("wakes: %d before the gap, %d in the five minutes after it, %d at five; want 0, 0, 1", beforeGap, afterGap, atFive)
	}
	if detail := lineStallWakes(t, h)[0].Detail; !strings.Contains(detail, "heavy_line_stalled: for 5m0s") {
		t.Errorf("wake %q; want the five minutes since the gap", detail)
	}
}

// MachineLineStall reads a line standing still from the machine's store,
// naming the line's lock a live process has held long, and reads nothing
// while a run holds the one turn's place or memory is short of the floor.
func TestMachineLineStallReadsAFreeTurnWithARunWaitingAndMemoryEnough(t *testing.T) {
	for _, tc := range []struct {
		name      string
		available uint64
		isStuck   bool
		want      []string
	}{
		{name: "the lock held long", available: 16 * gigabyte, isStuck: true, want: []string{"1 of 1 turns free and 1 in line, goblins gate test of a goblin closer to done first", "the line's lock (custody-mutation) has been held by pid"}},
		{name: "no lock held", available: 16 * gigabyte, want: []string{"1 of 1 turns free and 1 in line"}},
		{name: "memory short of the floor", available: gigabyte, isStuck: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			heavyLine(t, 1, 2*gigabyte)
			store, err := verify.AdmissionDir()
			if err != nil {
				t.Fatal(err)
			}
			var owner *lock.Info
			if tc.isStuck {
				owner = keepLineLock(t, store, time.Now().Add(-41*time.Minute))
			}

			// Act
			stall, err := MachineLineStall(Memory{Available: tc.available, CommitAvailable: tc.available, Total: 32 * gigabyte})

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if len(tc.want) == 0 && stall != "" {
				t.Errorf("MachineLineStall = %q; want nothing", stall)
			}
			for _, want := range tc.want {
				if !strings.Contains(stall, want) {
					t.Errorf("MachineLineStall = %q; want %q", stall, want)
				}
			}
			if owner != nil && len(tc.want) > 0 && !strings.Contains(stall, fmt.Sprintf("pid %d for 41m", owner.PID)) {
				t.Errorf("MachineLineStall = %q; want the lock's owner, pid %d, and its 41 minutes", stall, owner.PID)
			}
		})
	}
}

// lineStallWakes returns the queued wakes of a heavy line standing still.
func lineStallWakes(t *testing.T, h home.Home) []wake.Record {
	t.Helper()
	var stalls []wake.Record
	for _, record := range fleetWakeRecords(t, h, "idle") {
		if record.Key == "heavy-line" {
			stalls = append(stalls, record)
		}
	}
	return stalls
}

// keepLineLock writes the line's lock in store as this live process's, taken
// at since.
func keepLineLock(t *testing.T, store string, since time.Time) *lock.Info {
	t.Helper()
	self := t.TempDir()
	owner, err := lock.AcquireExclusiveNamedStrict(self, "custody-mutation")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.ReleaseExclusiveNamed(self, "custody-mutation") })
	owner.Acquired = since.UTC()
	data, err := json.Marshal(owner)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store, "custody-mutation")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	return owner
}

// A light run beside the slots holds none of the line's turns: with one
// heavy run and one light run holding, a line of two with a run waiting whose
// floor memory covers stands still with one of its two turns free.
func TestMachineLineStallCountsOnlyTheSlotsHoldersAgainstTheLine(t *testing.T) {
	// Arrange
	heavyLine(t, 2, 0)
	store, err := verify.AdmissionDir()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	heavy := verify.Admission{Dir: store, Available: func() (uint64, error) { return 64 * gigabyte, nil }, Who: "a heavy run", Estimate: gigabyte, Budget: time.Hour, Limit: time.Minute, Poll: 10 * time.Millisecond}
	slot, err := heavy.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer slot.Release()
	light := heavy
	light.IsLight, light.Who = true, "a light run"
	beside, err := light.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer beside.Release()
	waiter := heavy
	waiter.Who, waiter.Available, waiter.Limit = "a waiting run", func() (uint64, error) { return gigabyte, nil }, verify.NoLimit
	said := make(chan struct{}, 1)
	waiter.Waiting = func(time.Duration, string) {
		select {
		case said <- struct{}{}:
		default:
		}
	}
	waiting, stop := context.WithCancel(ctx)
	stopped := make(chan struct{})
	defer func() {
		stop()
		<-stopped
	}()
	go func() {
		defer close(stopped)
		_, _ = waiter.Wait(waiting)
	}()
	select {
	case <-said:
	case <-ctx.Done():
		t.Fatal("the waiting run never joined the line")
	}

	// Act
	stall, err := MachineLineStall(Memory{Available: 16 * gigabyte, CommitAvailable: 16 * gigabyte, Total: 32 * gigabyte})

	// Assert
	if err != nil || !strings.HasPrefix(stall, "1 of 2 turns free and 1 in line, a waiting run first") {
		t.Fatalf("MachineLineStall = %q, %v; want 1 of 2 turns free with the light run left out of the slots' count", stall, err)
	}
}
