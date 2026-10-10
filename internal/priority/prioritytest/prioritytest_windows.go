// Package prioritytest lets a test see the fleet's raise whatever priority
// class its binary was started at.
package prioritytest

import (
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// atStart is the priority class this test binary was started at, read before
// any test could raise it.
var atStart, _ = windows.GetPriorityClass(windows.CurrentProcess())

// FromNormal puts the test's own process at normal priority until the test
// ends. The fleet raises only a process at normal, and a child takes the
// class of a parent below it, so in a test binary started at another class,
// as a CI runner may start one, a test of the raise would see nothing and
// pass. It fails a test that finds the process still raised by a host or a
// control an earlier test left running in it, and skips only where the
// class cannot be changed at all, as under a job that sets one.
func FromNormal(t testing.TB) {
	t.Helper()
	process := windows.CurrentProcess()
	current := func() uint32 {
		class, err := windows.GetPriorityClass(process)
		if err != nil {
			t.Fatal(err)
		}
		return class
	}
	usual := current()
	if usual == windows.NORMAL_PRIORITY_CLASS {
		return
	}
	if usual == windows.ABOVE_NORMAL_PRIORITY_CLASS && atStart != windows.ABOVE_NORMAL_PRIORITY_CLASS {
		for deadline := time.Now().Add(20 * time.Second); current() != windows.NORMAL_PRIORITY_CLASS; time.Sleep(50 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("this process still runs at priority class %#x: an earlier test left a terminal's host or a control running in it, so no test here can see a raise made and given back", current())
			}
		}
		return
	}
	if err := windows.SetPriorityClass(process, windows.NORMAL_PRIORITY_CLASS); err != nil || current() != windows.NORMAL_PRIORITY_CLASS {
		t.Skipf("this process runs at priority class %#x and cannot be put at normal (%v), so it cannot see a raise from normal", usual, err)
	}
	t.Cleanup(func() {
		if err := windows.SetPriorityClass(process, usual); err != nil {
			t.Error(err)
		}
	})
}
