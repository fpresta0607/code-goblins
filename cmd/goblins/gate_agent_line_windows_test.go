package main

import (
	"context"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/verify"
)

// linePoll is how often goblins gate turn looks at the line again while it waits.
// A run reads its line limit once each time it looks, so a test that says a
// run still waits past a limit lets two looks go by first.
const linePoll = time.Second

// heldLine is a line of the test's own whose turn another run holds until
// the test lets the command "held" of the returned folder go.
func heldLine(t *testing.T) (slots, dir string, holder *turnRun) {
	t.Helper()
	slots = turnLine(t)
	dir = commandFolder(t)
	holder = startGateTurn(t, dir, nil, turnCommand(dir, "held", 0, true)...)
	eventually(t, "the holder's command starts", func() bool { return left(dir, "held", "started") })
	return slots, dir, holder
}

// A review fixer that asks for a turn for its checks, while another run holds
// the turn for longer than the fixer's budget lasts, leaves the line at its
// limit: its checks never start, the run that holds the turn keeps it, and
// its last line tells the fixer to commit its repair without them.
func TestAGateFixerWhoseBudgetWouldEndInTheLineLeavesItAndIsToldToCommit(t *testing.T) {
	// Arrange
	slots, dir, holder := heldLine(t)
	t.Setenv(gateAgentVariable, "1")
	runtime := turnRuntime()
	runtime.agentLineLimit = 300 * time.Millisecond
	var stdout, stderr lockedBuffer
	exited := make(chan int, 1)

	// Act
	go func() { exited <- gateTurn(runtime, &stdout, &stderr, turnCommand(dir, "checks", 0, false)...) }()
	exit := ended(t, exited, "the fixer's run, behind a turn held past its limit,")

	// Assert
	if want := "goblins gate turn: this run takes no turn: " + agentNoTurn; exit != exitNoTurn || lastLine(stderr.String()) != want || left(dir, "checks", "started") {
		t.Errorf("exit = %d, started = %v, last line = %q; want 125, no checks started, and %q", exit, left(dir, "checks", "started"), lastLine(stderr.String()), want)
	}
	holder.running(t, "the run that held the turn")
	if holding, waiting, err := verify.Line(slots); err != nil || len(holding) != 1 || len(waiting) != 0 {
		t.Errorf("the line holds %+v and %+v (%v); want the holder alone, with the fixer's run out of the line", holding, waiting, err)
	}
	letGo(t, dir, "held")
	holder.exit(t, "the run that held the turn")
}

// The limit is a gate agent's alone: a goblin's own run behind the same held
// turn waits on past it and runs its command once the turn is free.
func TestARunNoGateAgentStartedWaitsInLinePastAGateAgentsLimit(t *testing.T) {
	// Arrange
	_, dir, holder := heldLine(t)
	t.Setenv(gateAgentVariable, "")
	runtime := turnRuntime()
	runtime.agentLineLimit = 100 * time.Millisecond
	var stdout, stderr lockedBuffer
	exited := make(chan int, 1)

	// Act
	go func() { exited <- gateTurn(runtime, &stdout, &stderr, turnCommand(dir, "suite", 3, false)...) }()
	eventually(t, "the goblin's run says that it waits", func() bool {
		return strings.Contains(stderr.String(), "goblins gate turn: waiting for its turn (")
	})
	time.Sleep(2*linePoll + runtime.agentLineLimit)

	// Assert
	if len(exited) != 0 || left(dir, "suite", "started") {
		t.Fatalf("the goblin's run ended or started its command while the turn was held; stderr=%q", stderr.String())
	}
	letGo(t, dir, "held")
	holder.exit(t, "the run that held the turn")
	if exit := ended(t, exited, "the goblin's run, once the turn was free,"); exit != 3 || !left(dir, "suite", "ended") {
		t.Errorf("exit = %d, ended = %v; want its command's own 3; stderr=%q", exit, left(dir, "suite", "ended"), stderr.String())
	}
}

// Nor is the limit the gate's own Test step's, which its wrapper's --gate-run
// names: the gate waits on it under no agent's budget, so it waits on past
// the limit whatever its environment says, and runs its command once the turn
// is free. A Test step cut at the limit would leave its run with no checks.
func TestAGatesTestStepWaitsInLinePastAGateAgentsLimit(t *testing.T) {
	// Arrange
	_, comparison := gateEnvironmentFixture(t)
	_, dir, holder := heldLine(t)
	t.Setenv(gateAgentVariable, "1")
	runtime := turnRuntime()
	runtime.agentLineLimit = 100 * time.Millisecond
	var stdout, stderr lockedBuffer
	exited := make(chan int, 1)

	// Act
	go func() {
		exited <- gateTurn(runtime, &stdout, &stderr, append([]string{"--gate-run", "run", "--test-comparison-sha", comparison}, turnCommand(dir, "step", 3, false)...)...)
	}()
	eventually(t, "the Test step says that it waits", func() bool {
		return strings.Contains(stderr.String(), "goblins gate turn: waiting for its turn (")
	})
	time.Sleep(2*linePoll + runtime.agentLineLimit)

	// Assert
	if len(exited) != 0 || left(dir, "step", "started") {
		t.Fatalf("the Test step ended or started its command while the turn was held; stderr=%q", stderr.String())
	}
	letGo(t, dir, "held")
	holder.exit(t, "the run that held the turn")
	if exit := ended(t, exited, "the Test step, once the turn was free,"); exit != 3 || !left(dir, "step", "ended") {
		t.Errorf("exit = %d, ended = %v; want its command's own 3; stderr=%q", exit, left(dir, "step", "ended"), stderr.String())
	}
}

// goblins gate test stands in the same line, and a gate's agent that runs it
// leaves that line the same way, with no check run.
func TestAGateAgentsCeoGateTestLeavesTheLineAndRunsNoCheck(t *testing.T) {
	// Arrange
	module := testStepModule(t, nil, map[string]string{"a/a.go": "package a\n\nfunc A() int { return 2 }\n"})
	t.Chdir(module)
	t.Setenv(verify.TurnVariable, "")
	dir := commandFolder(t)
	holder := startGateTurn(t, dir, nil, turnCommand(dir, "held", 0, true)...)
	eventually(t, "the holder's command starts", func() bool { return left(dir, "held", "started") })
	t.Setenv(gateAgentVariable, "1")
	runtime := standIn()
	runtime.agentLineLimit = 300 * time.Millisecond
	var stdout, stderr lockedBuffer
	exited := make(chan int, 1)

	// Act
	go func() { exited <- gateTestWith(runtime, &stdout, &stderr) }()
	exit := ended(t, exited, "the agent's goblins gate test, behind a turn held past its limit,")

	// Assert
	if want := "goblins gate test: this run takes no turn: " + agentNoTurn + "\n"; exit != 1 || !strings.Contains(stderr.String(), want) || strings.Contains(stdout.String(), "ran go ") {
		t.Errorf("exit = %d, stdout = %q, stderr = %q; want 1, no check run, and %q", exit, stdout.String(), stderr.String(), want)
	}
	holder.running(t, "the run that held the turn")
	letGo(t, dir, "held")
	holder.exit(t, "the run that held the turn")
}

// So does goblins gate prepush, whose one line says that nothing ran and why.
func TestAGateAgentsCeoGatePrepushLeavesTheLineAndRunsNoCheck(t *testing.T) {
	// Arrange
	t.Chdir(prepushModule(t))
	t.Setenv(verify.TurnVariable, "")
	dir := commandFolder(t)
	holder := startGateTurn(t, dir, nil, turnCommand(dir, "held", 0, true)...)
	eventually(t, "the holder's command starts", func() bool { return left(dir, "held", "started") })
	t.Setenv(gateAgentVariable, "1")
	var checks atomic.Int32
	runtime := turnRuntime()
	runtime.gateDisk = roomy
	runtime.pushRun = func(context.Context, []string, string, []string, io.Writer, io.Writer) (int, error) {
		checks.Add(1)
		return 0, nil
	}
	runtime.agentLineLimit = 300 * time.Millisecond
	var stdout, stderr lockedBuffer
	exited := make(chan int, 1)

	// Act
	go func() { exited <- gatePrepushWith(runtime, &stdout, &stderr) }()
	exit := ended(t, exited, "the agent's goblins gate prepush, behind a turn held past its limit,")

	// Assert
	if want := "goblins gate prepush: nothing ran, since the machine gave this run no turn: " + agentNoTurn + ". CI is the only check of this push."; exit != 1 || lastLine(stdout.String()) != want || checks.Load() != 0 {
		t.Errorf("exit = %d after %d check(s), last line = %q; want 1, no check run, and %q", exit, checks.Load(), lastLine(stdout.String()), want)
	}
	holder.running(t, "the run that held the turn")
	letGo(t, dir, "held")
	holder.exit(t, "the run that held the turn")
}
