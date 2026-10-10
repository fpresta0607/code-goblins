package supervisor

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/priority"
)

// The supervisor runs one priority class above normal, and a run item's
// command, which is the Overlord's own and may be anything, must not: its
// start is one of the few that do not go through execx. Windows gives the
// child of a raised process the normal class as long as its start names no
// class.
func TestARunItemARaisedSupervisorStartsRunsAtNormal(t *testing.T) {
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
	shell := filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")

	// Act
	// A shell told to stay waits on its hidden console until it is ended,
	// and starts nothing of its own.
	started, err := runHidden(shell, []string{"/d", "/k"}, os.Getenv("SystemRoot"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ending, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, uint32(started.PID))
		if err != nil {
			return
		}
		_ = windows.TerminateProcess(ending, 1)
		_, _ = windows.WaitForSingleObject(ending, 10000)
		_ = windows.CloseHandle(ending)
	})
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(started.PID))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(process)
	class, err := windows.GetPriorityClass(process)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if class != windows.NORMAL_PRIORITY_CLASS {
		t.Errorf("the run item started by a raised process runs at priority class %#x, want normal, %#x", class, uint32(windows.NORMAL_PRIORITY_CLASS))
	}
}
