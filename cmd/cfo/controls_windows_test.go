package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// classAtStart is the priority class this test binary was started at, read
// before any test could raise it.
var classAtStart, _ = windows.GetPriorityClass(windows.CurrentProcess())

// classSeen records the priority class this process runs at each time a
// command writes to it, which is while the command is at work.
type classSeen struct {
	t *testing.T
	sync.Mutex
	classes []uint32
}

func (seen *classSeen) Write(written []byte) (int, error) {
	class, err := windows.GetPriorityClass(windows.CurrentProcess())
	if err != nil {
		seen.t.Error(err)
	}
	seen.Lock()
	defer seen.Unlock()
	seen.classes = append(seen.classes, class)
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

// fromNormal skips a test whose binary was started at another priority
// class, which cannot see a raise from normal, and fails one that finds this
// process still raised: a host or a control an earlier test left running in
// it would otherwise turn the tests of the raise into tests of nothing.
func fromNormal(t *testing.T) {
	t.Helper()
	if classAtStart != windows.NORMAL_PRIORITY_CLASS {
		t.Skipf("this test binary was started at priority class %#x, so it cannot see a control rise above normal", classAtStart)
	}
	for deadline := time.Now().Add(20 * time.Second); processClass(t) != windows.NORMAL_PRIORITY_CLASS; time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("this process still runs at priority class %#x: an earlier test left a terminal's host or a control running in it, so no test here can see a control raise it and give the class back", processClass(t))
		}
	}
}

// controlRun runs one of the fleet's controls far enough to say something,
// and exit is how it then ends: each is refused for its arguments or finds
// nothing to do in an empty home, so none starts, steers or stops anything.
type controlRun struct {
	args []string
	exit int
}

var controlRuns = map[string]controlRun{
	"serve":         {args: []string{"serve", "--listen", "not-a-loopback-address"}, exit: 2},
	"status":        {args: []string{"status", "one-argument-too-many"}, exit: 2},
	"stop":          {args: []string{"stop", "--no-such-flag"}, exit: 2},
	"attach":        {args: []string{"attach", "--no-such-flag"}, exit: 2},
	"update":        {args: []string{"update", "--no-such-flag"}, exit: 2},
	"process-plan":  {args: []string{"process-plan", "one-argument-too-many"}, exit: 2},
	"send":          {args: []string{"send"}, exit: 2},
	"answer":        {args: []string{"answer"}, exit: 2},
	"peek":          {args: []string{"peek"}, exit: 2},
	"fleet-view":    {args: []string{"fleet-view", "one-argument-too-many"}, exit: 2},
	"drain":         {args: []string{"drain"}, exit: 0},
	"watch":         {args: []string{"watch"}, exit: 1},
	"notify":        {args: []string{"notify"}, exit: 2},
	"question":      {args: []string{"question", "--no-such-flag"}, exit: 2},
	"register":      {args: []string{"register", "--no-such-flag"}, exit: 2},
	"pause":         {args: []string{"pause"}, exit: 2},
	"resume":        {args: []string{"resume", "g1", "--no-such-flag"}, exit: 2},
	"kill":          {args: []string{"kill"}, exit: 2},
	"hook":          {args: []string{"hook", "no-such-hook"}, exit: 0},
	"native-hook":   {args: []string{"native-hook"}, exit: 2},
	"session-start": {args: []string{"session-start"}, exit: 0},
}

// emptyHome is a home with nothing in it for a control to find, which is not
// primary, so cfo watch leaves at once. cfo serve forgets the variables of
// the terminal it was started in, so each one set is handed back when the
// test ends.
func emptyHome(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFO_HOME", root)
	for _, entry := range os.Environ() {
		if name, value, _ := strings.Cut(entry, "="); herdr.IsPaneVariable(name) || strings.EqualFold(name, host.IDVariable) || strings.EqualFold(name, host.ProofVariable) {
			t.Setenv(name, value)
		}
	}
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
	for name, run := range controlRuns {
		t.Run(name, func(t *testing.T) {
			// Arrange
			fromNormal(t)
			emptyHome(t)
			// A hook does nothing outside the CFO's own terminal.
			t.Setenv(host.IDVariable, supervisor.NativeCFOTerminal)
			said := &classSeen{t: t}

			// Act
			exit := runWithRuntime(run.args, said, said, defaultCommandRuntime())

			// Assert
			if exit != run.exit {
				t.Fatalf("cfo %v ended with %d, want %d: it did something other than what this test runs it for", run.args, exit, run.exit)
			}
			if len(said.classes) == 0 {
				t.Fatalf("cfo %v said nothing, so the test never saw it at work", run.args)
			}
			for _, class := range said.classes {
				if class != windows.ABOVE_NORMAL_PRIORITY_CLASS {
					t.Errorf("cfo %v worked at priority class %#x, want above normal, %#x", run.args, class, uint32(windows.ABOVE_NORMAL_PRIORITY_CLASS))
					break
				}
			}
			if after := processClass(t); after != windows.NORMAL_PRIORITY_CLASS {
				t.Errorf("after cfo %v this process runs at priority class %#x, want normal back, %#x", run.args, after, uint32(windows.NORMAL_PRIORITY_CLASS))
			}
		})
	}
}

// What a goblin starts in its terminal is its work, a control of the fleet
// included, and so is what a gate agent starts: neither is ever raised, so
// nothing the fleet supervises runs above anything else it supervises.
func TestNothingAGoblinOrAGateAgentStartsIsRaised(t *testing.T) {
	for who, variable := range map[string][2]string{
		"a goblin":     {harness.RoleVariable, harness.RoleGoblin},
		"a gate agent": {gateAgentVariable, "1"},
	} {
		for name, run := range controlRuns {
			t.Run(who+"/"+name, func(t *testing.T) {
				// Arrange
				fromNormal(t)
				emptyHome(t)
				t.Setenv(host.IDVariable, "g1")
				t.Setenv(variable[0], variable[1])
				said := &classSeen{t: t}

				// Act
				runWithRuntime(run.args, said, said, defaultCommandRuntime())

				// Assert
				for _, class := range said.classes {
					if class != windows.NORMAL_PRIORITY_CLASS {
						t.Errorf("cfo %v started by %s worked at priority class %#x, want normal, %#x", run.args, who, class, uint32(windows.NORMAL_PRIORITY_CLASS))
						break
					}
				}
			})
		}
	}
}

// A command that is not a control stays at normal for all of its work.
func TestACommandThatIsNotAControlStaysAtNormal(t *testing.T) {
	for name, args := range map[string][]string{
		"version": {"version"},
		"spawn":   {"spawn", "--no-such-flag"},
		"cleanup": {"cleanup", "--no-such-flag"},
		"gate":    {"gate"},
		"unknown": {"no-such-command"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			fromNormal(t)
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
