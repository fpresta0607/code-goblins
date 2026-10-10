package main

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// classSeen records the priority class this process runs at each time a
// command writes to it, which is while the command is at work.
type classSeen struct {
	t       *testing.T
	classes []uint32
}

func (seen *classSeen) Write(written []byte) (int, error) {
	seen.classes = append(seen.classes, processClass(seen.t))
	return len(written), nil
}

func processClass(t *testing.T) uint32 {
	t.Helper()
	class, err := windows.GetPriorityClass(windows.CurrentProcess())
	if err != nil {
		t.Fatal(err)
	}
	return class
}

// controlRuns runs each of the fleet's controls far enough to say something:
// each is refused for its arguments or finds nothing to do in an empty home,
// so none starts, steers or stops anything.
var controlRuns = map[string][]string{
	"serve":         {"serve", "--listen", "not-a-loopback-address"},
	"status":        {"status", "one-argument-too-many"},
	"stop":          {"stop", "--no-such-flag"},
	"attach":        {"attach", "--no-such-flag"},
	"update":        {"update", "--no-such-flag"},
	"send":          {"send"},
	"answer":        {"answer"},
	"peek":          {"peek"},
	"fleet-view":    {"fleet-view", "one-argument-too-many"},
	"drain":         {"drain"},
	"watch":         {"watch"},
	"notify":        {"notify"},
	"question":      {"question", "--no-such-flag"},
	"register":      {"register", "--no-such-flag"},
	"pause":         {"pause"},
	"resume":        {"resume", "g1", "--no-such-flag"},
	"kill":          {"kill"},
	"hook":          {"hook", "no-such-hook"},
	"native-hook":   {"native-hook"},
	"session-start": {"session-start"},
}

// emptyHome is a home with nothing in it for a control to find, which is not
// primary, so cfo watch leaves at once.
func emptyHome(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFO_HOME", root)
}

// The fleet's controls ran at the priority of the builds and tests they
// supervise. On 2026-10-10, with every core busy, cfo send twice got no
// answer to its handshake in five seconds and an update's prepare step took
// 56.7 of its 60 seconds. Beside sixteen busy threads that day, a host's
// handshake took 58 milliseconds at normal priority and 0.2 one class above,
// one reading of the machine's processes 7.7 seconds against 0.04, and one
// copy of a program, of which the prepare step makes five, 3.5 seconds
// against 0.07. Each control now runs one class above normal for as long as
// it works, and gives the class back.
func TestTheFleetsControlsRunAboveTheWorkAndGiveTheClassBack(t *testing.T) {
	if usual := processClass(t); usual != windows.NORMAL_PRIORITY_CLASS {
		t.Skipf("this test runs at priority class %#x, so it cannot see a control rise above the usual one", usual)
	}
	for name, args := range controlRuns {
		t.Run(name, func(t *testing.T) {
			// Arrange
			emptyHome(t)
			// A hook does nothing outside the CFO's own terminal.
			t.Setenv(host.IDVariable, supervisor.NativeCFOTerminal)
			said := &classSeen{t: t}

			// Act
			runWithRuntime(args, said, said, defaultCommandRuntime())

			// Assert
			if len(said.classes) == 0 {
				t.Fatalf("cfo %v said nothing, so the test never saw it at work", args)
			}
			for _, class := range said.classes {
				if class != windows.ABOVE_NORMAL_PRIORITY_CLASS {
					t.Errorf("cfo %v worked at priority class %#x, want above normal, %#x", args, class, uint32(windows.ABOVE_NORMAL_PRIORITY_CLASS))
					break
				}
			}
			if after := processClass(t); after != windows.NORMAL_PRIORITY_CLASS {
				t.Errorf("after cfo %v this process runs at priority class %#x, want normal back, %#x", args, after, uint32(windows.NORMAL_PRIORITY_CLASS))
			}
		})
	}
}

// What a goblin starts in its terminal is its work, a control of the fleet
// included, and so is what a gate agent starts: neither is ever raised, so
// nothing the fleet supervises runs above anything else it supervises.
func TestNothingAGoblinOrAGateAgentStartsIsRaised(t *testing.T) {
	if usual := processClass(t); usual != windows.NORMAL_PRIORITY_CLASS {
		t.Skipf("this test runs at priority class %#x, so it cannot tell a raise from the usual one", usual)
	}
	for who, variable := range map[string][2]string{
		"a goblin":     {harness.RoleVariable, harness.RoleGoblin},
		"a gate agent": {gateAgentVariable, "1"},
	} {
		for name, args := range controlRuns {
			t.Run(who+"/"+name, func(t *testing.T) {
				// Arrange
				emptyHome(t)
				t.Setenv(host.IDVariable, "g1")
				t.Setenv(variable[0], variable[1])
				said := &classSeen{t: t}

				// Act
				runWithRuntime(args, said, said, defaultCommandRuntime())

				// Assert
				for _, class := range said.classes {
					if class != windows.NORMAL_PRIORITY_CLASS {
						t.Errorf("cfo %v started by %s worked at priority class %#x, want normal, %#x", args, who, class, uint32(windows.NORMAL_PRIORITY_CLASS))
						break
					}
				}
			})
		}
	}
}

// A command that is not a control stays at normal for all of its work.
func TestACommandThatIsNotAControlStaysAtNormal(t *testing.T) {
	if usual := processClass(t); usual != windows.NORMAL_PRIORITY_CLASS {
		t.Skipf("this test runs at priority class %#x, so it cannot tell a raise from the usual one", usual)
	}
	for name, args := range map[string][]string{
		"version": {"version"},
		"spawn":   {"spawn", "--no-such-flag"},
		"cleanup": {"cleanup", "--no-such-flag"},
		"gate":    {"gate"},
		"unknown": {"no-such-command"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			emptyHome(t)
			said := &classSeen{t: t}

			// Act
			runWithRuntime(args, said, said, defaultCommandRuntime())

			// Assert
			if len(said.classes) == 0 {
				t.Fatalf("cfo %v said nothing, so the test never saw it at work", args)
			}
			for _, class := range said.classes {
				if class != windows.NORMAL_PRIORITY_CLASS {
					t.Errorf("cfo %v worked at priority class %#x, want normal, %#x", args, class, uint32(windows.NORMAL_PRIORITY_CLASS))
					break
				}
			}
		})
	}
}
