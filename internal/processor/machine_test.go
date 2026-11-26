//go:build windows || linux

package processor

import (
	"os"
	"testing"
)

// On this machine the fleet's work keeps off the last performance core at
// least, and is given no thread this test may not itself run on.
func TestMachineFleetThreadsLeavesThisMachinesAppsACore(t *testing.T) {
	// Arrange
	own := ownThreads(t)
	cores, err := MachineCores()
	if err != nil {
		t.Fatal(err)
	}
	fastest := uint8(0)
	for _, core := range cores {
		fastest = max(fastest, core.EfficiencyClass)
	}
	var performance []Core
	for _, core := range cores {
		if core.EfficiencyClass == fastest {
			performance = append(performance, core)
		}
	}
	var last uintptr
	for _, thread := range performance[len(performance)-1].Threads {
		last |= 1 << thread
	}
	if len(performance) < 2 || own&^last == 0 {
		if os.Getenv("CI") == "true" {
			t.Fatalf("CI runs this test on the threads %#x of a machine of %d performance cores, where the fleet's work has no core to leave, so this machine's reading went untested", own, len(performance))
		}
		t.Skipf("this test runs on the threads %#x of a machine of %d performance cores, where the fleet's work has no core to leave", own, len(performance))
	}

	// Act
	threads, err := MachineFleetThreads()

	// Assert
	t.Logf("this machine's fleet work keeps to the threads %#x of %#x", threads, own)
	if err != nil {
		t.Fatal(err)
	}
	if threads == 0 || threads&last != 0 || threads&^own != 0 {
		t.Fatalf("the fleet's work keeps to the threads %#x, want some of %#x and none of the last performance core's, %#x", threads, own, last)
	}
	var first uintptr
	for _, thread := range performance[0].Threads {
		first |= 1 << thread
	}
	if threads&first != own&first {
		t.Fatalf("the fleet's work keeps to the threads %#x of %#x, without the first performance core's, %#x, want those kept", threads, own, first)
	}
}
