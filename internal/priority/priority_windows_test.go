package priority

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

// classReport names the file a child of this test binary writes its own
// priority class to, instead of running the tests.
const classReport = "PRIORITY_TEST_CLASS_REPORT"

func TestMain(m *testing.M) {
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

// atNormal skips a test run by a process that is not at normal priority,
// which cannot see a raise from it.
func atNormal(t *testing.T) {
	t.Helper()
	if class := currentClass(t); class != windows.NORMAL_PRIORITY_CLASS {
		t.Skipf("this test runs at priority class %#x, so it cannot see a raise from normal", class)
	}
}

func TestAboveTheWorkRaisesThisProcessOneClassAndGivesItBack(t *testing.T) {
	// Arrange
	atNormal(t)

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
	atNormal(t)
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
			atNormal(t)
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
	atNormal(t)
	report := filepath.Join(t.TempDir(), "class")
	child := exec.Command(os.Args[0])
	child.Env = append(os.Environ(), classReport+"="+report)
	defer AboveTheWork()()

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
