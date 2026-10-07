package verify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/lock"
)

func admissionWithCapacity(t *testing.T, capacity int) Admission {
	t.Helper()
	a := admission(t)
	if err := os.WriteFile(filepath.Join(a.Dir, "capacity.json"), []byte(fmt.Sprintf(`{"capacity":%d}`, capacity)), 0o600); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestAdmissionSharedCapacityDeterminesTheMinimumFloor(t *testing.T) {
	for _, capacity := range []int{1, 2} {
		t.Run(fmt.Sprint(capacity), func(t *testing.T) {
			for _, sample := range []struct {
				name          string
				requested     uint64
				available     uint64
				shouldAcquire bool
			}{
				{"exact", 0, uint64(capacity) * 4 * gigabyte, true},
				{"one_byte_short", 0, uint64(capacity)*4*gigabyte - 1, false},
				{"lower_requested_floor", gigabyte, uint64(capacity)*4*gigabyte - 1, false},
				{"stricter_floor_short", 10 * gigabyte, 10*gigabyte - 1, false},
				{"stricter_floor_exact", 10 * gigabyte, 10 * gigabyte, true},
				{"zero_reading", 0, 0, false},
			} {
				t.Run(sample.name, func(t *testing.T) {
					// Arrange
					a := admissionWithCapacity(t, capacity)
					a.Floor, a.Limit = sample.requested, 20*time.Millisecond
					a.Available = func() (uint64, error) { return sample.available, nil }
					// A refusal must come from the 20 ms limit, never from this
					// bound, which only ends a run that hangs: a turn's lock
					// files alone can take a second on a loaded workstation.
					ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
					defer cancel()

					// Act
					turn, err := a.Wait(ctx)
					defer turn.Release()

					// Assert
					if sample.shouldAcquire {
						if err != nil || turn.release == nil {
							t.Fatalf("shared floor refused: %+v, %v", turn, err)
						}
					} else if err == nil || turn.release != nil || turn.Waited < a.Limit || ctx.Err() != nil {
						t.Fatalf("shared floor was bypassed: %+v, %v", turn, err)
					}
				})
			}
		})
	}
}

func TestAdmissionCapacityTwoPreservesCorruptSlotsAndWaiters(t *testing.T) {
	for _, area := range []string{"slot-1", "slot-2", "line"} {
		for name, data := range map[string][]byte{"empty": {}, "truncated": []byte(`{"pid":`), "missing_identity": []byte(`{}`)} {
			t.Run(area+"/"+name, func(t *testing.T) {
				// Arrange
				a := admissionWithCapacity(t, 2)
				file := filepath.Join(a.Dir, area)
				if area == "line" {
					file = filepath.Join(a.Dir, "line", "00000000000000000001-1-1")
					if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(file, data, 0o600); err != nil {
					t.Fatal(err)
				}
				old := time.Now().Add(-24 * time.Hour)
				if err := os.Chtimes(file, old, old); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
				defer cancel()

				// Act
				turn, err := a.Wait(ctx)
				defer turn.Release()

				// Assert
				kept, readErr := os.ReadFile(file)
				if err == nil || turn.release != nil || readErr != nil || string(kept) != string(data) {
					t.Fatalf("capacity two lost uncertain custody: %+v, %v, %v", turn, err, readErr)
				}
			})
		}
	}
}

func TestAdmissionCapacityTwoKeepsUnverifiableIdentityInCustody(t *testing.T) {
	for _, slot := range []string{"slot-1", "slot-2"} {
		t.Run(slot, func(t *testing.T) {
			// Arrange
			a := admissionWithCapacity(t, 2)
			record := lock.Info{PID: 4242, OwnerPID: 4242, Hostname: "another-machine", Start: time.Now().Add(-3 * time.Hour), Acquired: time.Now().Add(-2 * time.Hour)}
			data, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(a.Dir, slot)
			if err := os.WriteFile(file, data, 0o600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()

			// Act
			turn, err := a.Wait(ctx)
			defer turn.Release()

			// Assert
			kept, readErr := os.ReadFile(file)
			if !errors.Is(err, context.DeadlineExceeded) || turn.release != nil || readErr != nil || string(kept) != string(data) {
				t.Fatalf("capacity two bypassed unverifiable identity: %+v, %v, %v", turn, err, readErr)
			}
		})
	}
}

func TestAdmissionCapacityTwoReclaimsOnlyTheGoneProcessIdentity(t *testing.T) {
	for _, slot := range []string{"slot-1", "slot-2"} {
		t.Run(slot, func(t *testing.T) {
			// Arrange
			a := admissionWithCapacity(t, 2)
			first := within(t, take(t, a), "first holder")
			defer first.Release()
			second := within(t, take(t, a), "second holder")
			defer second.Release()
			hostname, err := os.Hostname()
			if err != nil {
				t.Fatal(err)
			}
			gone := lock.Info{PID: os.Getpid(), OwnerPID: os.Getpid(), Hostname: hostname, Start: time.Now().Add(-24 * time.Hour), Acquired: time.Now().Add(-2 * time.Hour)}
			data, err := json.Marshal(gone)
			if err != nil {
				t.Fatal(err)
			}
			if slot == "slot-1" {
				first.Release()
			} else {
				second.Release()
			}
			if err := os.WriteFile(filepath.Join(a.Dir, slot), data, 0o600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()

			// Act
			turn, waitErr := a.Wait(ctx)
			defer turn.Release()

			// Assert
			current, readErr := lock.ReadNamedStrict(a.Dir, slot)
			holding, _, lineErr := Line(a.Dir)
			if waitErr != nil || turn.release == nil || readErr != nil || !current.VerifiedAlive() || *current == gone || lineErr != nil || len(holding) != 2 {
				t.Fatalf("gone identity was not replaced in its exact slot: %v, %+v, %v, %+v, %v", waitErr, current, readErr, holding, lineErr)
			}
		})
	}
}

func TestAdmissionCapacityTwoPreservesCustodyOutsideItsTwoSlots(t *testing.T) {
	for _, slot := range []string{"slot-0", "slot-3", "slot-01"} {
		t.Run(slot, func(t *testing.T) {
			// Arrange
			a := admissionWithCapacity(t, 2)
			data := []byte(`{"unknown":"outside shared capacity"}`)
			file := filepath.Join(a.Dir, slot)
			if err := os.WriteFile(file, data, 0o600); err != nil {
				t.Fatal(err)
			}

			// Act
			turn, err := a.Wait(context.Background())
			defer turn.Release()

			// Assert
			kept, readErr := os.ReadFile(file)
			if err == nil || turn.release != nil || readErr != nil || string(kept) != string(data) {
				t.Fatalf("outside-capacity custody was lost or bypassed: %+v, %v, %v", turn, err, readErr)
			}
		})
	}
}

func TestAdmissionCapacityTwoNeverExceedsTwoConcurrentHolders(t *testing.T) {
	// Arrange
	a := admissionWithCapacity(t, 2)
	blocked := make(chan struct{}, 1)
	a.Waiting = func(_ time.Duration, why string) {
		if strings.HasSuffix(why, "this run is next in line") {
			select {
			case blocked <- struct{}{}:
			default:
			}
		}
	}
	// The one bound is for a run that hangs. The six turns come one after
	// another, each writing and reading lock files, which a busy machine can
	// slow to tenths of a second a file: six took up to 25 seconds on a
	// loaded workstation without anything being wrong.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	leave := make(chan struct{})
	var runs sync.WaitGroup
	var held, maximum, finished atomic.Int32

	// Act
	for range 6 {
		runs.Add(1)
		go func() {
			defer runs.Done()
			turn, err := a.Wait(ctx)
			if err != nil {
				t.Error(err)
				return
			}
			count := held.Add(1)
			for seen := maximum.Load(); count > seen && !maximum.CompareAndSwap(seen, count); seen = maximum.Load() {
			}
			select {
			case <-leave:
			case <-ctx.Done():
			}
			held.Add(-1)
			turn.Release()
			finished.Add(1)
		}()
	}
	select {
	case <-blocked:
	case <-ctx.Done():
		t.Error("no waiter reached the occupied shared slots")
	}
	close(leave)
	runs.Wait()

	// Assert
	if maximum.Load() != 2 || finished.Load() != 6 || held.Load() != 0 {
		t.Fatalf("concurrent capacity changed: maximum=%d finished=%d held=%d", maximum.Load(), finished.Load(), held.Load())
	}
}

func TestAdmissionCapacityTwoPreservesFIFOAndExactRelease(t *testing.T) {
	// Arrange
	a := admissionWithCapacity(t, 2)
	first := within(t, take(t, a), "first holder")
	defer first.Release()
	second := within(t, take(t, a), "second holder")
	defer second.Release()
	var early, late said
	a.Waiting = early.waiting
	earlier := take(t, a)
	early.waits(t, earlier, "earlier waiter")
	a.Waiting = late.waiting
	later := take(t, a)
	late.waits(t, later, "later waiter")
	before, err := os.ReadFile(filepath.Join(a.Dir, "slot-1"))
	if err != nil {
		t.Fatal(err)
	}

	// Act
	second.Release()
	current := within(t, earlier, "earlier waiter after the second slot returns")
	defer current.Release()
	second.Release()

	// Assert
	notYet(t, later, "later waiter before the earlier exact lease returns")
	after, readErr := os.ReadFile(filepath.Join(a.Dir, "slot-1"))
	currentOwner, ownerErr := lock.ReadNamedStrict(a.Dir, "slot-2")
	if readErr != nil || string(before) != string(after) || ownerErr != nil || !currentOwner.VerifiedAlive() {
		t.Fatalf("exact release changed other custody: %v, %v", readErr, ownerErr)
	}
	current.Release()
	within(t, later, "later waiter after exact release").Release()
}

func TestAdmissionCapacityTwoCancellationPreservesBothHoldersAndTheNextWaiter(t *testing.T) {
	// Arrange
	a := admissionWithCapacity(t, 2)
	first := within(t, take(t, a), "first holder")
	defer first.Release()
	second := within(t, take(t, a), "second holder")
	defer second.Release()
	before := map[string]string{}
	for _, slot := range []string{"slot-1", "slot-2"} {
		data, err := os.ReadFile(filepath.Join(a.Dir, slot))
		if err != nil {
			t.Fatal(err)
		}
		before[slot] = string(data)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	joined := make(chan struct{}, 1)
	a.Who = "cancelled waiter"
	a.Waiting = func(time.Duration, string) {
		select {
		case joined <- struct{}{}:
		default:
		}
	}
	ended := make(chan error, 1)
	go func() { turn, err := a.Wait(ctx); turn.Release(); ended <- err }()
	select {
	case <-joined:
	case <-time.After(time.Second):
		t.Fatal("cancelled waiter did not join")
	}
	var survivor said
	a.Who, a.Waiting = "surviving waiter", survivor.waiting
	waiting := take(t, a)
	survivor.waits(t, waiting, "surviving waiter")

	// Act
	cancel()
	cancelErr := <-ended

	// Assert
	for slot, data := range before {
		kept, err := os.ReadFile(filepath.Join(a.Dir, slot))
		if err != nil || string(kept) != data {
			t.Fatalf("cancellation changed %s: %v", slot, err)
		}
	}
	holding, inLine, lineErr := Line(a.Dir)
	if !errors.Is(cancelErr, context.Canceled) || lineErr != nil || len(holding) != 2 || len(inLine) != 1 || inLine[0].Who != "surviving waiter" {
		t.Fatalf("cancellation changed the common line: %v, %+v, %+v, %v", cancelErr, holding, inLine, lineErr)
	}
	second.Release()
	within(t, waiting, "surviving waiter after release").Release()
}
