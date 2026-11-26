package processor

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ownAndMachineThreads are the masks of the processor threads this test may
// run on and of every thread of the machine's processor group.
func ownAndMachineThreads(t *testing.T) (own, machine uintptr) {
	t.Helper()
	if result, _, err := getProcessAffinityMask.Call(uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&own)), uintptr(unsafe.Pointer(&machine))); result == 0 {
		t.Fatal(err)
	}
	return own, machine
}

// ownThreads is the mask of the processor threads this test may run on.
func ownThreads(t *testing.T) uintptr {
	t.Helper()
	own, _ := ownAndMachineThreads(t)
	return own
}

func TestMachineCoresNamesEachOfThisMachinesThreadsOnce(t *testing.T) {
	// Arrange
	_, machine := ownAndMachineThreads(t)

	// Act
	cores, err := MachineCores()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	var named uintptr
	for _, core := range cores {
		for _, thread := range core.Threads {
			if named&(1<<thread) != 0 {
				t.Fatalf("thread %d is named by two cores of %+v", thread, cores)
			}
			named |= 1 << thread
		}
	}
	if named != machine {
		t.Fatalf("the cores name the threads %#x, want this machine's, %#x", named, machine)
	}
}
