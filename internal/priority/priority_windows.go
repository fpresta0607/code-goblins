// Package priority runs the fleet's own short work one priority class above
// the work it supervises.
package priority

import (
	"sync"

	"golang.org/x/sys/windows"
)

// raised counts the pieces of work this process runs above the work at once,
// so the last one to end gives the normal class back.
var raised struct {
	sync.Mutex
	count int
}

// AboveTheWork raises this process one priority class above normal for as
// long as a control of the fleet runs, and returns what gives the class
// back. A goblin's builds and tests fill the cores at normal priority, and
// a control at that priority waits its turn behind them after each of its
// system calls: beside sixteen busy threads on 2026-10-10, one reading of
// the machine's processes took 7.7 seconds at normal priority and 0.04 above
// it. Above normal is one class up and never more: it comes before a
// compiler, and a control's work is too short for a person's own apps to
// wait on. Beside the same threads a plain process launch took 1.10 seconds
// with the controls at work raised and 1.22 with them at normal.
//
// Only a process at normal priority is raised. One that runs higher needs
// nothing, and one that runs lower was put there on purpose and hands its
// class to what it starts, which a raise would undo. Windows starts the
// child of a raised process at normal, so nothing a control starts is raised
// with it.
func AboveTheWork() (restore func()) {
	raised.Lock()
	defer raised.Unlock()
	process := windows.CurrentProcess()
	if raised.count == 0 {
		usual, err := windows.GetPriorityClass(process)
		if err != nil || usual != windows.NORMAL_PRIORITY_CLASS {
			return func() {}
		}
		if err := windows.SetPriorityClass(process, windows.ABOVE_NORMAL_PRIORITY_CLASS); err != nil {
			return func() {}
		}
	}
	raised.count++
	return func() {
		raised.Lock()
		defer raised.Unlock()
		if raised.count--; raised.count == 0 {
			_ = windows.SetPriorityClass(process, windows.NORMAL_PRIORITY_CLASS)
		}
	}
}
