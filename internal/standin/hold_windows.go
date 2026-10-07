package standin

import (
	"testing"

	"golang.org/x/sys/windows"
)

// Hold opens pid, a process the test started and that runs now, and keeps it
// open until the test ends, when it ends the process through that handle.
// Check whether it runs by the handle Hold returns, never by opening pid
// again: Windows gives an ended process's pid to a later process once no
// handle to the ended one is left, so a check or an end by pid after the
// process may have ended reaches whatever took the pid, and an end leaves
// that program exiting 1 with nothing printed. A held process keeps its pid
// until the test is over, so neither a check nor the end reaches any other
// program.
//
// Cleanups run last registered first. Hold after RemoveAtCleanup for the
// folder the process runs from, so the process is ended before the folder is
// removed.
func Hold(t testing.TB, pid int) windows.Handle {
	t.Helper()
	process, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		t.Fatalf("standin: hold pid %d: %v", pid, err)
	}
	t.Cleanup(func() {
		_ = windows.TerminateProcess(process, 1)
		_, _ = windows.WaitForSingleObject(process, 10000)
		_ = windows.CloseHandle(process)
	})
	return process
}
