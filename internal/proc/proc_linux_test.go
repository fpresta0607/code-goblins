package proc

import (
	"encoding/binary"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// The ticks a process's start is counted in are read as hundredths of a
// second, which the kernel tells every process it starts.
func TestTheKernelCountsAProcesssStartInHundredthsOfASecond(t *testing.T) {
	// Arrange
	const clockTicksEntry = 17
	raw, err := os.ReadFile("/proc/self/auxv")
	if err != nil {
		t.Fatal(err)
	}

	// Act
	var ticks uint64
	for ; len(raw) >= 16; raw = raw[16:] {
		if binary.NativeEndian.Uint64(raw) == clockTicksEntry {
			ticks = binary.NativeEndian.Uint64(raw[8:])
		}
	}

	// Assert
	if ticks != ticksPerSecond {
		t.Errorf("the kernel counts %d ticks to the second, and a start is read as %d", ticks, ticksPerSecond)
	}
}

// A start is pinned to the clock around the spawn, which no reading of
// /proc gives: the machine's start is cut to the second and a process's
// ticks to the tick, so a start reads early by less than a second and a
// tick, and never late.
func TestAChildsStartLiesBetweenTheSecondBeforeItsSpawnAndTheMomentAfterIt(t *testing.T) {
	// Arrange
	before := time.Now()
	child, _ := startSleeper(t, t.TempDir())
	after := time.Now()

	// Act
	start, isFound := StartTime(child.Process.Pid)

	// Assert
	if !isFound {
		t.Fatal("the child's start could not be read")
	}
	if earliest := before.Add(-time.Second - time.Second/ticksPerSecond); start.Before(earliest) || start.After(after) {
		t.Errorf("the child reads as started at %s, want between %s and %s", start.Format(time.RFC3339Nano), earliest.Format(time.RFC3339Nano), after.Format(time.RFC3339Nano))
	}
}

// An instance is the machine's boot ID and the process's start in ticks, as
// the kernel gives them, with no clock in either. proc(5) counts the start
// as the 22nd field of a process's line, and a program named sleep has no
// space in its name to move the count.
func TestAnInstanceIsTheBootIDAndTheStartTicksOfItsProcess(t *testing.T) {
	// Arrange
	child, _ := startSleeper(t, t.TempDir())
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		t.Fatal(err)
	}
	line, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", child.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(line))
	if len(fields) < 22 {
		t.Fatalf("the child's line %q has no 22nd field", line)
	}
	want := strings.TrimSpace(string(boot)) + "/" + fields[21]

	// Act
	instance, err := Instance(child.Process.Pid)
	again, againErr := Instance(child.Process.Pid)

	// Assert
	if err != nil || againErr != nil {
		t.Fatalf("Instance = %v, then %v", err, againErr)
	}
	if instance != want || again != want {
		t.Errorf("two readings of the child's instance gave %q and %q, want %q", instance, again, want)
	}
	if strings.ContainsAny(instance, " \t\r\n") {
		t.Errorf("the instance %q holds a space, and a waiter's ticket parts its fields at one", instance)
	}
}
