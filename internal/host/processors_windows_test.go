package host

import (
	"os"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/processor"
)

// processorThreads is the mask of the processor threads the process pid
// names may run on.
func processorThreads(t *testing.T, pid int) uintptr {
	t.Helper()
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(process)
	threads, err := processor.ProcessThreads(process)
	if err != nil {
		t.Fatal(err)
	}
	return threads
}

// The fleet's work took every processor core at the priority of the
// Overlord's own apps (processor.fleetThreads has the measurement). A host
// told to leave cores for his apps keeps its terminal to the fleet's threads,
// through the command line a host is launched with, and any other host leaves
// its terminal on the threads the host itself may run on.
func TestAHostToldToLeaveCoresForAppsKeepsItsTerminalOffThem(t *testing.T) {
	own := processorThreads(t, os.Getpid())
	fleet, err := processor.MachineFleetThreads()
	if err != nil {
		t.Fatal(err)
	}
	if fleet == 0 || fleet == own {
		if os.Getenv("CI") == "true" {
			t.Fatalf("CI runs this test on the processor threads %#x, where the fleet's work keeps to %#x, so a terminal kept off the apps' cores went untested", own, fleet)
		}
		t.Skipf("this test runs on the processor threads %#x, where the fleet's work keeps to %#x, as inside a goblin's terminal, so what a host sets cannot be told from what it inherits", own, fleet)
	}
	for _, test := range []struct {
		name                    string
		shouldLeaveCoresForApps bool
		want                    uintptr
	}{
		{"a host told to leave cores for apps", true, fleet},
		{"any other host", false, own},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			spec := Spec{ID: "g1", Args: []string{os.Args[0], "echo-child"}, Cols: 80, Rows: 25, ShouldLeaveCoresForApps: test.shouldLeaveCoresForApps}

			// Act
			record, err := Launch(t.TempDir(), []string{os.Args[0]}, hostEnvironment(), spec)
			if err != nil {
				t.Fatalf("Launch: %v", err)
			}
			pin(t, record.HostPID)
			t.Cleanup(func() { end(record.HostPID) })
			pin(t, record.ChildPID)

			// Assert
			if got := processorThreads(t, record.ChildPID); got != test.want {
				t.Fatalf("the terminal's process may run on the processor threads %#x, want %#x", got, test.want)
			}
		})
	}
}
