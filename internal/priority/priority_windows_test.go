package priority

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/priority/prioritytest"
)

// classReport names the file a child of this test binary writes its own
// priority class to, instead of running the tests, and a child given
// waitReport waits a minute to be raised and ended instead.
const (
	classReport = "PRIORITY_TEST_CLASS_REPORT"
	waitReport  = "PRIORITY_TEST_WAIT"
)

func TestMain(m *testing.M) {
	if os.Getenv(waitReport) != "" {
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	if report := os.Getenv(classReport); report != "" {
		class, err := windows.GetPriorityClass(windows.CurrentProcess())
		if err != nil || os.WriteFile(report, fmt.Appendf(nil, "%#x", class), 0o600) != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func currentClass(t *testing.T) uint32 {
	t.Helper()
	class, err := windows.GetPriorityClass(windows.CurrentProcess())
	if err != nil {
		t.Fatal(err)
	}
	return class
}

func TestAboveTheWorkRaisesThisProcessOneClassAndGivesItBack(t *testing.T) {
	// Arrange
	prioritytest.FromNormal(t)

	// Act
	restore := AboveTheWork()
	during := currentClass(t)
	restore()

	// Assert
	if during != windows.ABOVE_NORMAL_PRIORITY_CLASS {
		t.Errorf("while raised this process ran at priority class %#x, want above normal, %#x, and never higher", during, uint32(windows.ABOVE_NORMAL_PRIORITY_CLASS))
	}
	if after := currentClass(t); after != windows.NORMAL_PRIORITY_CLASS {
		t.Errorf("after the work this process runs at priority class %#x, want normal back, %#x", after, uint32(windows.NORMAL_PRIORITY_CLASS))
	}
}

// A supervisor answers the board while its janitor reads the machine: the
// first piece of work to end must not take the class from the other.
func TestTheClassIsKeptUntilTheLastPieceOfWorkEnds(t *testing.T) {
	// Arrange
	prioritytest.FromNormal(t)
	first := AboveTheWork()
	second := AboveTheWork()

	// Act
	first()
	whileOneRuns := currentClass(t)
	second()

	// Assert
	if whileOneRuns != windows.ABOVE_NORMAL_PRIORITY_CLASS {
		t.Errorf("with one piece of work still running this process ran at priority class %#x, want above normal, %#x", whileOneRuns, uint32(windows.ABOVE_NORMAL_PRIORITY_CLASS))
	}
	if after := currentClass(t); after != windows.NORMAL_PRIORITY_CLASS {
		t.Errorf("after the last piece of work this process runs at priority class %#x, want normal, %#x", after, uint32(windows.NORMAL_PRIORITY_CLASS))
	}
}

// A process below normal was put there on purpose and hands its class to
// what it starts. Raising it would start its children at normal, above where
// their parent was told to run.
func TestAProcessThatIsNotAtNormalIsLeftAsItIs(t *testing.T) {
	for name, class := range map[string]uint32{
		"below normal": windows.BELOW_NORMAL_PRIORITY_CLASS,
		"high":         windows.HIGH_PRIORITY_CLASS,
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			prioritytest.FromNormal(t)
			if err := windows.SetPriorityClass(windows.CurrentProcess(), class); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := windows.SetPriorityClass(windows.CurrentProcess(), windows.NORMAL_PRIORITY_CLASS); err != nil {
					t.Error(err)
				}
			})

			// Act
			restore := AboveTheWork()
			during := currentClass(t)
			restore()

			// Assert
			if during != class {
				t.Errorf("a process at priority class %#x was moved to %#x", class, during)
			}
			if after := currentClass(t); after != class {
				t.Errorf("a process at priority class %#x was left at %#x", class, after)
			}
		})
	}
}

// Windows gives a new process the normal class unless the process that
// starts it is below normal or idle, so nothing a raised control starts is
// raised with it: a host's harness, a command's git, a supervisor's pause.
func TestWhatARaisedProcessStartsRunsAtNormal(t *testing.T) {
	// Arrange
	prioritytest.FromNormal(t)
	report := filepath.Join(t.TempDir(), "class")
	child := exec.Command(os.Args[0])
	child.Env = append(os.Environ(), classReport+"="+report)
	defer AboveTheWork()()
	if raised := currentClass(t); raised != windows.ABOVE_NORMAL_PRIORITY_CLASS {
		t.Fatalf("this process runs at priority class %#x, so the child is not started by a raised process", raised)
	}

	// Act
	output, err := child.CombinedOutput()

	// Assert
	if err != nil {
		t.Fatalf("the child failed: %v: %s", err, output)
	}
	got, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("%#x", uint32(windows.NORMAL_PRIORITY_CLASS)); string(got) != want {
		t.Errorf("the child of a raised process ran at priority class %s, want normal, %s", got, want)
	}
}

// A control something waits on starts one class above the work from its
// creation, from a process at normal or already raised, and a process below
// normal starts it where that process runs, as it starts everything.
func TestStartAboveTheWorkStartsAControlOneClassAboveNormal(t *testing.T) {
	tests := []struct {
		name        string
		parent      uint32
		shouldRaise bool
		want        uint32
	}{
		{name: "from normal", parent: windows.NORMAL_PRIORITY_CLASS, want: windows.ABOVE_NORMAL_PRIORITY_CLASS},
		{name: "from a raised control", parent: windows.NORMAL_PRIORITY_CLASS, shouldRaise: true, want: windows.ABOVE_NORMAL_PRIORITY_CLASS},
		{name: "from below normal", parent: windows.BELOW_NORMAL_PRIORITY_CLASS, want: windows.BELOW_NORMAL_PRIORITY_CLASS},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			prioritytest.FromNormal(t)
			if err := windows.SetPriorityClass(windows.CurrentProcess(), test.parent); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := windows.SetPriorityClass(windows.CurrentProcess(), windows.NORMAL_PRIORITY_CLASS); err != nil {
					t.Error(err)
				}
			})
			if test.shouldRaise {
				defer AboveTheWork()()
			}
			report := filepath.Join(t.TempDir(), "class")
			child := exec.Command(os.Args[0])
			child.Env = append(os.Environ(), classReport+"="+report)
			child.SysProcAttr = &syscall.SysProcAttr{CreationFlags: StartAboveTheWork()}

			// Act
			output, err := child.CombinedOutput()

			// Assert
			if err != nil {
				t.Fatalf("the child failed: %v: %s", err, output)
			}
			got, err := os.ReadFile(report)
			if err != nil {
				t.Fatal(err)
			}
			if want := fmt.Sprintf("%#x", test.want); string(got) != want {
				t.Errorf("the child ran at priority class %s, want %s", got, want)
			}
		})
	}
}
