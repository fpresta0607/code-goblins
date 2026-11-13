//go:build linux || darwin

package lock

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/proc"
)

// holdersRecord writes dir/name as the lock records pid, a process that runs
// on this machine, after change has altered the record, and returns it.
func holdersRecord(t *testing.T, dir, name string, pid int, change func(*Info)) *Info {
	t.Helper()
	record, status := ownerInfo(pid, "watch")
	if status != statusAlive {
		t.Fatalf("process %d could not be read to record it", pid)
	}
	change(record)
	if err := writeInfo(context.Background(), filepath.Join(dir, name), record); err != nil {
		t.Fatal(err)
	}
	return record
}

// cutInstance parts an instance into the machine's boot ID and the ticks
// after it at which the process was created.
func cutInstance(t *testing.T, instance string) (boot string, ticks uint64) {
	t.Helper()
	boot, counted, isCut := strings.Cut(instance, "/")
	ticks, err := strconv.ParseUint(counted, 10, 64)
	if !isCut || boot == "" || err != nil {
		t.Fatalf("the instance %q is no boot ID and ticks", instance)
	}
	return boot, ticks
}

// A step of the clock moves the start every process reads at, by a second
// for any step that carries the machine's start across one. A holder that
// ran through the step is the process its record names all the same: the
// record is known by its instance, and its lock is not taken from it.
func TestALiveHolderKeepsItsLockWhenItsStartReadsDifferently(t *testing.T) {
	for _, moved := range []time.Duration{-time.Second, time.Second, time.Hour} {
		t.Run(moved.String(), func(t *testing.T) {
			// Arrange
			dir, name := t.TempDir(), ".watch.lock"
			holder := liveProcess(t)
			record := holdersRecord(t, dir, name, holder, func(record *Info) { record.Start = record.Start.Add(moved) })

			// Act
			_, err := AcquireNamedOwner(dir, name, os.Getpid(), "watch")

			// Assert
			if !errors.Is(err, ErrHeld) {
				t.Errorf("the lock of a live holder whose start reads %s off was asked for and gave %v, want ErrHeld", moved, err)
			}
			if kept, err := ReadNamed(dir, name); err != nil || kept.PID != holder {
				t.Errorf("the lock records %+v, %v; want its live holder, pid %d, left in it", kept, err, holder)
			}
			if !record.Alive() || !record.VerifiedAlive() || !HeldByNamed(dir, name, holder) {
				t.Errorf("a live holder whose start reads %s off: Alive %t, VerifiedAlive %t, HeldByNamed %t, want all true", moved, record.Alive(), record.VerifiedAlive(), HeldByNamed(dir, name, holder))
			}
		})
	}
}

// The holder itself reads its start a second off after such a step, and
// still lets go of the lock it holds.
func TestAHolderReleasesItsLockWhenItsStartReadsDifferently(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	holdersRecord(t, dir, ".lock", os.Getpid(), func(record *Info) { record.Start = record.Start.Add(time.Second) })

	// Act
	err := Release(dir)

	// Assert
	if err != nil {
		t.Fatalf("the holder could not let go of its lock: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".lock")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the lock's record is still there after its holder let go: %v", err)
	}
}

// A record names one run of a program. A process that has the recorded ID
// and even the recorded start, but is another instance, is not the holder:
// the holder is gone, and its lock is taken back.
func TestARecordOfAnotherInstanceIsTakenAsADeadHolders(t *testing.T) {
	tests := []struct {
		name     string
		instance func(boot string, ticks uint64) string
	}{
		{name: "the same ticks after another start of the machine", instance: func(_ string, ticks uint64) string {
			return "00000000-0000-0000-0000-000000000000/" + strconv.FormatUint(ticks, 10)
		}},
		{name: "a later process of this start of the machine", instance: func(boot string, ticks uint64) string {
			return boot + "/" + strconv.FormatUint(ticks+1, 10)
		}},
		{name: "none beside a start that matches", instance: func(string, uint64) string { return "" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			dir, name := t.TempDir(), ".watch.lock"
			holder := liveProcess(t)
			record := holdersRecord(t, dir, name, holder, func(record *Info) {
				record.Instance = test.instance(cutInstance(t, record.Instance))
			})

			// Act
			info, err := AcquireNamedOwner(dir, name, os.Getpid(), "watch")

			// Assert
			if err != nil || info.PID != os.Getpid() {
				t.Fatalf("the lock of a holder that is gone was asked for and gave %+v, %v; want it taken", info, err)
			}
			if record.Alive() || record.VerifiedAlive() {
				t.Errorf("a record of another instance: Alive %t, VerifiedAlive %t, want both false", record.Alive(), record.VerifiedAlive())
			}
			if err := ReleaseNamed(dir, name); err != nil {
				t.Errorf("the lock taken back could not be let go: %v", err)
			}
		})
	}
}

// What a holder records as its instance is what the kernel says of it, so
// every other process reads the same of the holder.
func TestARecordNamesItsHoldersInstance(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	want, err := proc.Instance(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}

	// Act
	_, err = Acquire(dir)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if recorded, err := Read(dir); err != nil || recorded.Instance != want {
		t.Errorf("the lock records %+v, %v; want the instance %q", recorded, err, want)
	}
	if err := Release(dir); err != nil {
		t.Errorf("the lock could not be let go: %v", err)
	}
}
