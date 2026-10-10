package lifecycle

import (
	"sync"

	"golang.org/x/sys/windows"
)

// raised counts the teardowns this process runs at once and keeps the
// priority class it had before the first of them, so the last one to end
// gives it back.
var raised struct {
	sync.Mutex
	count int
	usual uint32
}

// aboveTheWork raises this process above the work a teardown ends, for as
// long as the teardown runs, and returns what gives its priority back. A
// teardown has seconds to read every process on the machine and end what is
// the task's, and at the priority of that work it waits its turn behind it:
// on 2026-10-10, with six goblins building and testing, one reading of some
// 500 processes took between 0.04 and 3.9 seconds at normal priority and
// 0.02 every time above it, and a stop ran out of its ten seconds having
// ended everything but not read that it had. A memory pause is taken when
// the machine is at its busiest. Above normal is one class up: it comes
// before a compiler and after nothing a person is using needs. A process
// that already runs higher, or whose priority cannot be set, runs as it is.
func aboveTheWork() (restore func()) {
	raised.Lock()
	defer raised.Unlock()
	process := windows.CurrentProcess()
	if raised.count == 0 {
		usual, err := windows.GetPriorityClass(process)
		if err != nil || usual != windows.NORMAL_PRIORITY_CLASS && usual != windows.BELOW_NORMAL_PRIORITY_CLASS && usual != windows.IDLE_PRIORITY_CLASS {
			return func() {}
		}
		if err := windows.SetPriorityClass(process, windows.ABOVE_NORMAL_PRIORITY_CLASS); err != nil {
			return func() {}
		}
		raised.usual = usual
	}
	raised.count++
	return func() {
		raised.Lock()
		defer raised.Unlock()
		if raised.count--; raised.count == 0 {
			_ = windows.SetPriorityClass(process, raised.usual)
		}
	}
}
