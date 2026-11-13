package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/verify"
)

// A gate ends an agent whose invocation ran past its budget, and counts the
// agent's wait for the machine's turn in it. On 2026-11-02 two review fixers
// each spent their whole hour in line, and were ended with their repairs
// written and not committed (issue 1932). These tests hold the line's side of
// it: a run a gate's agent started waits a bounded time for its turn and is
// then told to go on without it, and every other run waits as it did.

// agentNoTurn is what a run a gate's agent started says once 300ms brought
// it no turn.
const agentNoTurn = "no turn came within 300ms, which is as long as a run a gate's agent starts waits in line, since its gate counts the wait against the agent's invocation budget. Go on without this run and start nothing heavy outside the line: commit the work, and the gate runs the project's checks in a turn of its own"

// ended is the exit code of a run the test started in this process, which
// must end within a minute.
func ended(t *testing.T, exited <-chan int, what string) int {
	t.Helper()
	select {
	case exit := <-exited:
		return exit
	case <-time.After(time.Minute):
		t.Fatalf("%s had not ended after a minute", what)
		return -1
	}
}

// The limit is the run's and not each check's. goblins gate test takes a turn for
// each of its checks, so a limit that began again with every check would have
// one run of several checks wait several limits, most of an agent's budget
// (issue 1932). A run whose first check's turn came only as its whole limit
// passed waits for no later turn: its next check takes a turn that is free,
// and behind another run it leaves the line at once, saying nothing of a
// wait, with the first check run and the next never started.
func TestAGateAgentsCeoGateTestHasOneLineLimitAcrossItsChecks(t *testing.T) {
	for name, isBehindAnotherRun := range map[string]bool{
		"its next check leaves the line behind another run": true,
		"its next check takes a turn that is free":          false,
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			module := testStepModule(t, nil, map[string]string{"a/a.go": "package a\n\nfunc A() int { return 2 }\n"})
			t.Chdir(module)
			t.Setenv(verify.TurnVariable, "")
			t.Setenv(gateAgentVariable, "1")
			const limit = 300 * time.Millisecond
			runtime := standIn()
			runtime.agentLineLimit = limit
			// The run's first read of the machine's memory, which its first
			// check's turn waits on, lasts the run's whole limit.
			var readings atomic.Int32
			runtime.availableMemory = func() (supervisor.Memory, error) {
				if readings.Add(1) == 1 {
					time.Sleep(limit)
				}
				return plenty()
			}
			// go vet, the run's first check, lasts until the test lets it end.
			vetStarted, vetMayEnd := make(chan struct{}), make(chan struct{})
			runtime.gateRun = func(command []string, _ string, _ []string, stdout, _ io.Writer) (int, error) {
				if command[1] == "vet" {
					close(vetStarted)
					<-vetMayEnd
				}
				fmt.Fprintf(stdout, "ran go %s\n", command[1])
				return 0, nil
			}
			var stdout, stderr lockedBuffer
			exited := make(chan int, 1)

			// Act
			// The affected level tests every package the change reaches, so
			// the run has two checks: go vet, then go test.
			go func() { exited <- gateTestWith(runtime, &stdout, &stderr, "--level", "affected") }()
			select {
			case <-vetStarted:
			case exit := <-exited:
				t.Fatalf("the run ended with exit %d before its first check started; stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
			case <-time.After(time.Minute):
				t.Fatalf("the run's first check had not started after a minute; stdout=%q stderr=%q", stdout.String(), stderr.String())
			}
			if isBehindAnotherRun {
				// Another run joins the line while the first check holds the
				// turn, so it stands ahead of the run's next check.
				waitInLine(t, verify.Admission{Dir: filepath.Join(os.Getenv("GOBLINS_VERIFY_DIR"), "slots"), Floor: 4 << 30, Available: func() (uint64, error) { return 1 << 30, nil }, Who: "a run that waits", Limit: time.Minute, Poll: 10 * time.Millisecond})
			}
			close(vetMayEnd)
			exit := ended(t, exited, "the agent's goblins gate test, its limit spent by its first check's wait,")

			// Assert
			said := stdout.String()
			hasLeftLine := strings.Contains(stderr.String(), "goblins gate test: this run takes no turn: "+agentNoTurn+"\n")
			ranTests := strings.Contains(said, "ran go test\n")
			wantExit := map[bool]int{true: 1, false: 0}[isBehindAnotherRun]
			if exit != wantExit || !strings.Contains(said, "ran go vet\n") || ranTests == isBehindAnotherRun || hasLeftLine != isBehindAnotherRun {
				t.Errorf("exit = %d, tests run = %v, left the line = %v, stdout = %q, stderr = %q; want exit %d with go vet run, and the tests run and the line left only as %v and %v say", exit, ranTests, hasLeftLine, said, stderr.String(), wantExit, !isBehindAnotherRun, isBehindAnotherRun)
			}
			if strings.Contains(said, "goblins gate test: waiting for its turn") {
				t.Errorf("stdout = %q; want no wait said, since the first check's wait left the next none of the run's limit", said)
			}
		})
	}
}

// Only a run a gate's agent started has the limit, which is three minutes on
// the machine's own runtime. The gate's own Test step has none, whatever its
// environment says, since the gate waits on it under no agent's budget.
func TestOnlyARunAGateAgentStartedHasALineLimit(t *testing.T) {
	runtime := defaultCommandRuntime()
	for name, test := range map[string]struct {
		agent      string
		isTestStep bool
		want       time.Duration
	}{
		"a goblin's own run":                  {"", false, 0},
		"a gate agent's run":                  {"1", false, 3 * time.Minute},
		"the gate's Test step":                {"", true, 0},
		"the gate's Test step under an agent": {"1", true, 0},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			getenv := func(name string) string {
				if name == gateAgentVariable {
					return test.agent
				}
				return ""
			}

			// Act
			got := gateLineLimit(runtime, getenv, test.isTestStep)

			// Assert
			if got != test.want {
				t.Errorf("the run's line limit = %s; want %s", got, test.want)
			}
		})
	}
}
