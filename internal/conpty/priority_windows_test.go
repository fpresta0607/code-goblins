package conpty

import (
	"errors"
	"os"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/priority"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

// A terminal's host runs one priority class above normal, and everything it
// starts for its console must not: the console's program is a goblin's
// harness, and neither start here goes through execx. Windows gives the
// child of a raised process the normal class as long as its start names no
// class, so the program, its console server and its input waker all run at
// normal.
func TestWhatARaisedHostStartsForItsConsoleRunsAtNormal(t *testing.T) {
	// Arrange
	usual, err := windows.GetPriorityClass(windows.CurrentProcess())
	if err != nil {
		t.Fatal(err)
	}
	if usual != windows.NORMAL_PRIORITY_CLASS {
		t.Skipf("this test runs at priority class %#x, so it cannot raise itself from normal", usual)
	}
	defer priority.AboveTheWork()()
	if raised, err := windows.GetPriorityClass(windows.CurrentProcess()); err != nil || raised != windows.ABOVE_NORMAL_PRIORITY_CLASS {
		t.Fatalf("this process runs at priority class %#x (%v), so nothing here is started by a raised process", raised, err)
	}

	// Act
	console, _ := startChild(t, Spec{Cols: 80, Rows: 25})
	processes, err := proc.Processes()
	if err != nil {
		t.Fatal(err)
	}
	classes := map[int]uint32{}
	for _, process := range processes {
		if process.ParentPID != os.Getpid() {
			continue
		}
		handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(process.PID))
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			continue // An earlier test's process that has ended since the list.
		}
		if err != nil {
			t.Fatal(err)
		}
		class, err := windows.GetPriorityClass(handle)
		windows.CloseHandle(handle)
		if err != nil {
			t.Fatal(err)
		}
		classes[process.PID] = class
	}

	// Assert
	// The program, its console server and its input waker are three.
	if _, isRead := classes[console.PID()]; !isRead || len(classes) < 3 {
		t.Fatalf("read the priority class of %d processes this one started, the console's program among them: %t; want the program, its console server and its input waker", len(classes), isRead)
	}
	for pid, class := range classes {
		if class != windows.NORMAL_PRIORITY_CLASS {
			t.Errorf("pid %d, started by this raised process, runs at priority class %#x, want normal, %#x", pid, class, uint32(windows.NORMAL_PRIORITY_CLASS))
		}
	}
}
