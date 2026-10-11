package main

import (
	"slices"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
)

// A report runs under the goblin's own tool call, which ends with it: the
// report, the shell it runs in and the shells above it are never what the
// goblin left running. What runs beside them is, and a machine service the
// goblin started is not, since it serves every goblin.
func TestWhatIsLeftRunningIsBesideTheReport(t *testing.T) {
	// Arrange
	placed := func(pid, parent int, isService bool) fleettree.Placed {
		return fleettree.Placed{Process: fleettree.Process{PID: pid, ParentPID: parent}, Service: isService}
	}
	processes := []fleettree.Placed{
		placed(10, 1, false),  // the tool call's shell, under the harness
		placed(11, 10, false), // the report itself
		placed(12, 10, false), // a server the same shell left in the background
		placed(20, 1, false),  // another background shell
		placed(21, 20, false), // its watch
		placed(30, 990, true), // the gate daemon the goblin started
		placed(31, 30, true),  // a gate's agent under it
	}

	// Act
	left := leftBeside(processes, 11)

	// Assert
	var pids []int
	for _, process := range left {
		pids = append(pids, process.PID)
	}
	if want := []int{12, 20, 21}; !slices.Equal(pids, want) {
		t.Errorf("left running = %v, want %v", pids, want)
	}
}
