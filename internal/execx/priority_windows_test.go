package execx

import (
	"fmt"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/priority"
)

// The fleet's controls run one priority class above normal, and what they
// start must not: a host starts a goblin's harness, a command starts git, and
// the supervisor starts a pause. Windows gives the child of a raised process
// the normal class as long as its start names no class, which no start
// through this package does.
func TestWhatARaisedProcessStartsThroughExecxRunsAtNormal(t *testing.T) {
	usual, err := windows.GetPriorityClass(windows.CurrentProcess())
	if err != nil {
		t.Fatal(err)
	}
	if usual != windows.NORMAL_PRIORITY_CLASS {
		t.Skipf("this test runs at priority class %#x, so it cannot raise itself from normal", usual)
	}
	for _, entry := range []string{"run", "start", "command"} {
		t.Run(entry, func(t *testing.T) {
			// Arrange
			report := filepath.Join(t.TempDir(), "report")
			// The child fixture reads where to report from its environment,
			// which it takes from this process.
			t.Setenv("EXECX_CONSOLE_REPORT", report)
			defer priority.AboveTheWork()()
			if raised, err := windows.GetPriorityClass(windows.CurrentProcess()); err != nil || raised != windows.ABOVE_NORMAL_PRIORITY_CLASS {
				t.Fatalf("this process runs at priority class %#x (%v), so nothing here is started by a raised process", raised, err)
			}

			// Act
			code := startFixtureChild(entry, "class", report)

			// Assert
			if code != 0 {
				t.Fatal("the child never reported its priority class")
			}
			got, err := fsx.ReadFile(report)
			if err != nil {
				t.Fatal(err)
			}
			if want := fmt.Sprintf("class=%#x", uint32(windows.NORMAL_PRIORITY_CLASS)); string(got) != want {
				t.Errorf("the child of a raised process reported %s, want %s, the normal class", got, want)
			}
		})
	}
}
