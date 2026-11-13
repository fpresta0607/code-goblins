package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/execx/treetest"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/standin"
	"github.com/fpresta0607/code-goblins/internal/verify"
)

// turnRun is goblins gate turn running as a process of its own.
type turnRun struct {
	process        *exec.Cmd
	stdout, stderr *lockedBuffer
	exited         chan int
}

// startGateTurn starts goblins gate turn in dir with args, as a process of its
// own that env is added to the environment of. A run still going when the
// test ends is ended there.
func startGateTurn(t *testing.T, dir string, env []string, args ...string) *turnRun {
	t.Helper()
	run := &turnRun{process: exec.Command(os.Args[0], append([]string{gateTurnTestWrapper}, args...)...), stdout: &lockedBuffer{}, stderr: &lockedBuffer{}, exited: make(chan int, 1)}
	run.process.Dir, run.process.Env = dir, append(os.Environ(), env...)
	// A console of its own, with no window, so a console event the run sends,
	// by design or by a fault, reaches the run and what it starts, and never
	// the console the tests run in.
	run.process.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	run.process.Stdout, run.process.Stderr = run.stdout, run.stderr
	// A command that outlives its wrapper keeps the wrapper's output open,
	// and the wait for the wrapper does not wait for the command.
	run.process.WaitDelay = 2 * time.Second
	if err := run.process.Start(); err != nil {
		t.Fatal(err)
	}
	ended := make(chan struct{})
	go func() {
		defer close(ended)
		run.process.Wait()
		run.exited <- run.process.ProcessState.ExitCode()
	}()
	t.Cleanup(func() {
		run.process.Process.Kill()
		<-ended
	})
	return run
}

// exit waits for the run to end and returns its exit code.
func (run *turnRun) exit(t *testing.T, what string) int {
	t.Helper()
	select {
	case code := <-run.exited:
		return code
	case <-time.After(time.Minute):
		t.Fatalf("%s had not ended after a minute; stdout=%q stderr=%q", what, run.stdout.String(), run.stderr.String())
		return -1
	}
}

// running fails the test when the run has ended.
func (run *turnRun) running(t *testing.T, what string) {
	t.Helper()
	select {
	case code := <-run.exited:
		t.Fatalf("%s ended with exit %d; stdout=%q stderr=%q", what, code, run.stdout.String(), run.stderr.String())
	default:
	}
}

// With the turn free and memory to spare, the wrapper adds nothing: standard
// output and standard error are the command's bytes and no others, and the
// exit code is the command's, whatever it is.
func TestGateTurnPassesTheCommandsOutputAndExitCodeThrough(t *testing.T) {
	for _, code := range []int{0, 3, 125} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			// Arrange
			turnLine(t)
			dir := commandFolder(t)

			// Act
			run := startGateTurn(t, "", nil, turnCommand(dir, "command", code, false)...)
			exit := run.exit(t, "the run")

			// Assert
			if exit != code || run.stdout.String() != commandOutput("command") || run.stderr.String() != commandErrors("command") {
				t.Errorf("exit = %d, stdout = %q, stderr = %q; want the command's %d, %q and %q", exit, run.stdout.String(), run.stderr.String(), code, commandOutput("command"), commandErrors("command"))
			}
		})
	}
}

// One heavy run at a time: a second goblins gate turn started while the first
// one's command runs says who holds the turn, starts nothing, and starts its
// own command only once the first one's has ended. What it prints of its own
// goes to standard error before the command's, and standard output is the
// command's alone.
func TestGateTurnsRunTheirCommandsOneAtATime(t *testing.T) {
	// Arrange
	turnLine(t)
	dir := commandFolder(t)
	first := startGateTurn(t, dir, nil, append([]string{"--as", "the first run"}, turnCommand(dir, "first", 3, true)...)...)
	eventually(t, "the first run's command starts", func() bool { return left(dir, "first", "started") })

	// Act
	second := startGateTurn(t, dir, nil, turnCommand(dir, "second", 5, false)...)

	// Assert
	held := fmt.Sprintf("the turn is held by the first run: %s (pid %d), for ", strings.Join(turnCommand(dir, "first", 3, true)[1:], " "), first.process.Process.Pid)
	eventually(t, "the second run says who holds the turn: "+held, func() bool { return strings.Contains(second.stderr.String(), held) })
	// The wait has to reach a second to show when the turn is taken.
	time.Sleep(1200 * time.Millisecond)
	second.running(t, "the second run, while the first held the turn,")
	if left(dir, "second", "started") {
		t.Fatal("the second run's command started while the first run's command ran")
	}
	if waiting := second.stderr.String(); !strings.HasPrefix(waiting, "goblins gate turn: waiting for its turn (") || !strings.Contains(waiting, "; this run is next in line\n") || strings.Contains(waiting, " budget") {
		t.Errorf("while it waited the second run said %q; want lines that start with goblins gate turn: and say where it stands in line, and no budget for a holder that has none", waiting)
	}
	letGo(t, dir, "first")
	if exit := first.exit(t, "the first run"); exit != 3 {
		t.Errorf("the first run's exit = %d; want its command's 3", exit)
	}
	if exit := second.exit(t, "the second run"); exit != 5 {
		t.Errorf("the second run's exit = %d; want its command's 5; stderr=%q", exit, second.stderr.String())
	}
	if !left(dir, "second", "ended") || left(dir, "second", "overlap") {
		t.Errorf("the second run's command ended = %v, and found another command running = %v; want it to have run alone", left(dir, "second", "ended"), left(dir, "second", "overlap"))
	}
	if second.stdout.String() != commandOutput("second") {
		t.Errorf("the second run's stdout = %q; want its command's alone, %q", second.stdout.String(), commandOutput("second"))
	}
	said := second.stderr.String()
	took := strings.Index(said, "goblins gate turn: took its turn after ")
	if took < 0 || !strings.HasSuffix(said, "s\n"+commandErrors("second")) || strings.Count(said, "goblins gate turn: took its turn after ") != 1 {
		t.Errorf("the second run's stderr = %q; want one line saying it took its turn, then its command's %q and nothing after", said, commandErrors("second"))
	}
	if holding, waiting, err := verify.Line(filepath.Join(os.Getenv("GOBLINS_VERIFY_DIR"), "slots")); err != nil || len(holding) != 0 || len(waiting) != 0 {
		t.Errorf("after both runs the line holds %+v and %+v (%v); want it empty", holding, waiting, err)
	}
}

// A holder that died frees the line: when the process that holds the turn is
// ended mid-run, the run behind it takes the turn and starts its command. No
// lock a dead run left ever stops the machine's tests.
func TestAGateTurnWhoseHolderWasEndedMidRunTakesTheTurn(t *testing.T) {
	// Arrange
	slots := turnLine(t)
	dir := commandFolder(t)
	first := startGateTurn(t, dir, nil, turnCommand(dir, "first", 0, true)...)
	eventually(t, "the first run's command starts", func() bool { return left(dir, "first", "started") })
	second := startGateTurn(t, dir, nil, turnCommand(dir, "second", 7, false)...)
	held := fmt.Sprintf("(pid %d), for ", first.process.Process.Pid)
	eventually(t, "the second run says it waits for the first: "+held, func() bool { return strings.Contains(second.stderr.String(), held) })
	if left(dir, "second", "started") {
		t.Fatal("the second run's command started while the first run held the turn")
	}

	// Act
	if err := first.process.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	first.exit(t, "the first run, once ended,")

	// Assert
	if exit := second.exit(t, "the second run, once the holder was ended,"); exit != 7 || !left(dir, "second", "ended") {
		t.Errorf("the second run's exit = %d and its command ended = %v; want its command's 7 after the dead holder's turn was taken; stderr=%q", exit, left(dir, "second", "ended"), second.stderr.String())
	}
	if holding, waiting, err := verify.Line(slots); err != nil || len(holding) != 0 || len(waiting) != 0 {
		t.Errorf("after the second run the line holds %+v and %+v (%v); want it empty", holding, waiting, err)
	}
}

// A run given a wait that takes no turn within it runs nothing, exits 125 and
// says why, and leaves the line to the run that holds it.
func TestGateTurnRunsNothingWhenNoTurnComesWithinItsWait(t *testing.T) {
	// Arrange
	slots := turnLine(t)
	dir := commandFolder(t)
	first := startGateTurn(t, dir, nil, turnCommand(dir, "first", 0, true)...)
	eventually(t, "the first run's command starts", func() bool { return left(dir, "first", "started") })

	// Act
	second := startGateTurn(t, dir, nil, append([]string{"--wait", "1s"}, turnCommand(dir, "second", 7, false)...)...)
	exit := second.exit(t, "the run whose wait ran out")

	// Assert
	if want := "goblins gate turn: this run takes no turn: it took no turn within its wait of 1s"; exit != exitNoTurn || !strings.Contains(second.stderr.String(), want) || left(dir, "second", "started") {
		t.Errorf("exit = %d, started = %v, stderr = %q; want 125, nothing started, and %q", exit, left(dir, "second", "started"), second.stderr.String(), want)
	}
	if holding, waiting, err := verify.Line(slots); err != nil || len(holding) != 1 || len(waiting) != 0 {
		t.Errorf("the line holds %+v and %+v (%v); want the first run alone", holding, waiting, err)
	}
	letGo(t, dir, "first")
	first.exit(t, "the first run")
}

// A run whose turn comes within its wait runs its command until it ends,
// however long that outlasts the wait.
func TestGateTurnRunsItsCommandPastItsWaitOnceItHasItsTurn(t *testing.T) {
	// Arrange
	turnLine(t)
	dir := commandFolder(t)
	run := startGateTurn(t, dir, nil, append([]string{"--wait", "1s"}, turnCommand(dir, "command", 7, true)...)...)
	eventually(t, "the command starts", func() bool { return left(dir, "command", "started") })

	// Act
	time.Sleep(2 * time.Second)
	run.running(t, "the run, past its wait with its turn taken,")
	letGo(t, dir, "command")
	exit := run.exit(t, "the run")

	// Assert
	if exit != 7 || !left(dir, "command", "ended") {
		t.Errorf("exit = %d, ended = %v; want the command's own 7; stderr=%q", exit, left(dir, "command", "ended"), run.stderr.String())
	}
	// A wait that still counted once the turn was taken would leave the
	// execution's record failed as interrupted, which the run would say.
	if strings.Contains(run.stderr.String(), "goblins gate turn:") {
		t.Errorf("stderr = %q; want no line of goblins gate turn's own from a run whose turn came at once", run.stderr.String())
	}
}

// Memory under the floor holds a run back, physical or commit: it says what
// is available and what the floor is, starts nothing, and starts its command
// once the machine has the memory.
func TestGateTurnHoldsItsCommandBackWhileMemoryIsUnderTheFloor(t *testing.T) {
	for _, short := range []string{"physical-short", "commit-short"} {
		t.Run(short, func(t *testing.T) {
			// Arrange
			turnLine(t)
			dir := commandFolder(t)
			marker := filepath.Join(t.TempDir(), short)
			if err := os.WriteFile(marker, nil, 0o644); err != nil {
				t.Fatal(err)
			}

			// Act
			run := startGateTurn(t, dir, []string{gateTurnTestMemoryVariable + "=" + marker}, turnCommand(dir, "command", 4, false)...)

			// Assert
			want := "): 3.0 GB of memory is available and the floor is 4.0 GB; this run is next in line\n"
			eventually(t, "the run says what it waits for: "+want, func() bool { return strings.Contains(run.stderr.String(), want) })
			time.Sleep(300 * time.Millisecond)
			run.running(t, "the run, with memory under the floor,")
			if left(dir, "command", "started") {
				t.Fatal("the command started with memory under the floor")
			}
			if err := os.Remove(marker); err != nil {
				t.Fatal(err)
			}
			if exit := run.exit(t, "the run, once the machine had the memory,"); exit != 4 || !left(dir, "command", "ended") {
				t.Errorf("exit = %d and the command ended = %v; want the command's 4; stderr=%q", exit, left(dir, "command", "ended"), run.stderr.String())
			}
		})
	}
}

// goblins gate test and goblins gate turn stand in one line. A goblins gate test started
// while a goblins gate turn's command runs names that run and starts no check
// until the command has ended.
func TestGateTestWaitsForTheTurnOfAGateTurn(t *testing.T) {
	// Arrange
	module := testStepModule(t, nil, map[string]string{"a/a.go": "package a\n\nfunc A() int { return 2 }\n"})
	t.Chdir(module)
	t.Setenv(verify.TurnVariable, "")
	t.Setenv("GOBLINS_TASK_ID", "")
	dir := commandFolder(t)
	holder := startGateTurn(t, dir, nil, append([]string{"--as", "another repository's tests"}, turnCommand(dir, "held", 0, true)...)...)
	eventually(t, "the holder's command starts", func() bool { return left(dir, "held", "started") })

	// Act
	var stdout, stderr lockedBuffer
	exited := make(chan int, 1)
	go func() { exited <- gateTestWith(standIn(), &stdout, &stderr) }()

	// Assert
	want := fmt.Sprintf("the turn is held by another repository's tests: %s (pid %d), for ", strings.Join(turnCommand(dir, "held", 0, true)[1:], " "), holder.process.Process.Pid)
	eventually(t, "goblins gate test says who holds the turn: "+want, func() bool {
		return strings.Contains(stdout.String(), "goblins gate test: waiting for its turn (") && strings.Contains(stdout.String(), want)
	})
	time.Sleep(300 * time.Millisecond)
	if strings.Contains(stdout.String(), "ran go ") || len(exited) != 0 {
		t.Fatalf("goblins gate test went on while a goblins gate turn's command ran; stdout=%q", stdout.String())
	}
	letGo(t, dir, "held")
	select {
	case exit := <-exited:
		if exit != 0 || !strings.Contains(stdout.String(), "ran go test") {
			t.Errorf("exit = %d; want 0 with the tests run once the turn was free; stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
		}
	case <-time.After(time.Minute):
		t.Fatalf("goblins gate test had not ended a minute after the turn was free; stdout=%q", stdout.String())
	}
}

// And the other way round: a goblins gate turn started while a goblins gate test runs
// its checks names that run, with its budget, and starts its command only
// once the checks have ended.
func TestGateTurnWaitsForTheTurnOfAGateTest(t *testing.T) {
	// Arrange
	module := testStepModule(t, nil, map[string]string{"a/a.go": "package a\n\nfunc A() int { return 2 }\n"})
	t.Chdir(module)
	t.Setenv(verify.TurnVariable, "")
	t.Setenv("GOBLINS_TASK_ID", "")
	dir := commandFolder(t)
	checking, finish := make(chan struct{}), make(chan struct{})
	finished := sync.OnceFunc(func() { close(finish) })
	t.Cleanup(finished)
	runtime := standIn()
	runtime.gateRun = func(command []string, _ string, _ []string, _, _ io.Writer) (int, error) {
		if command[1] == "test" {
			close(checking)
			<-finish
		}
		return 0, nil
	}
	var stdout, stderr lockedBuffer
	exited := make(chan int, 1)
	go func() { exited <- gateTestWith(runtime, &stdout, &stderr) }()
	select {
	case <-checking:
	case <-time.After(time.Minute):
		t.Fatalf("goblins gate test had not started its tests after a minute; stdout=%q stderr=%q", stdout.String(), stderr.String())
	}

	// Act
	run := startGateTurn(t, dir, nil, turnCommand(dir, "command", 2, false)...)

	// Assert
	held := fmt.Sprintf(" (pid %d), for ", os.Getpid())
	eventually(t, "the run says goblins gate test holds the turn", func() bool {
		said := run.stderr.String()
		return strings.Contains(said, "goblins gate turn: waiting for its turn (") && strings.Contains(said, "the turn is held by m at ") && strings.Contains(said, held) && strings.Contains(said, " of its 1h30m0s budget; this run is next in line\n")
	})
	time.Sleep(300 * time.Millisecond)
	run.running(t, "the run, while goblins gate test ran its tests,")
	if left(dir, "command", "started") {
		t.Fatal("the run's command started while goblins gate test ran its tests")
	}
	finished()
	if exit := run.exit(t, "the run, once goblins gate test had ended,"); exit != 2 || !left(dir, "command", "ended") {
		t.Errorf("the run's exit = %d; want its command's 2; stderr=%q", exit, run.stderr.String())
	}
	select {
	case exit := <-exited:
		if exit != 0 {
			t.Errorf("goblins gate test's exit = %d; want 0; stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
		}
	case <-time.After(time.Minute):
		t.Fatalf("goblins gate test had not ended a minute after its tests; stdout=%q", stdout.String())
	}
}

// This repository's own test command is goblins gate test, which takes a turn
// for each of its checks. Behind goblins gate turn it runs inside the turn taken
// for it, and does not wait for that turn, which it would wait on for good.
func TestGateTestBehindGateTurnRunsInsideItsTurn(t *testing.T) {
	// Arrange
	module := testStepModule(t, nil, map[string]string{"a/a.go": "package a\n\nfunc A() int { return 2 }\n"})
	t.Setenv(verify.TurnVariable, "")
	t.Setenv("GOBLINS_TASK_ID", "")

	// Act
	run := startGateTurn(t, module, nil, "--", os.Args[0], gateTurnTestGateTest)
	exit := run.exit(t, "goblins gate test behind goblins gate turn")

	// Assert
	printed := run.stdout.String() + run.stderr.String()
	if exit != 0 || strings.Contains(printed, "waiting for its turn") || strings.Contains(printed, "took its turn") {
		t.Errorf("exit = %d and the run printed %q; want 0 with no wait for a turn", exit, printed)
	}
	for _, want := range []string{"ran go vet\n", "ran go test\n", "goblins gate test: passed at level fast in "} {
		if !strings.Contains(run.stdout.String(), want) {
			t.Errorf("stdout %q lacks %q", run.stdout.String(), want)
		}
	}
}

// goblins gate prepush stands in the same line: started while a goblins gate turn's
// command runs, it names that run and starts no check until the command has
// ended.
func TestGatePrepushWaitsForTheTurnOfAGateTurn(t *testing.T) {
	// Arrange
	t.Chdir(prepushModule(t))
	t.Setenv(verify.TurnVariable, "")
	t.Setenv("GOBLINS_TASK_ID", "")
	dir := commandFolder(t)
	holder := startGateTurn(t, dir, nil, append([]string{"--as", "another repository's tests"}, turnCommand(dir, "held", 0, true)...)...)
	eventually(t, "the holder's command starts", func() bool { return left(dir, "held", "started") })
	var checks atomic.Int32
	runtime := turnRuntime()
	runtime.gateDisk = roomy
	runtime.pushRun = func(context.Context, []string, string, []string, io.Writer, io.Writer) (int, error) {
		checks.Add(1)
		return 0, nil
	}

	// Act
	var stdout, stderr lockedBuffer
	exited := make(chan int, 1)
	go func() { exited <- gatePrepushWith(runtime, &stdout, &stderr) }()

	// Assert
	want := fmt.Sprintf("the turn is held by another repository's tests: %s (pid %d), for ", strings.Join(turnCommand(dir, "held", 0, true)[1:], " "), holder.process.Process.Pid)
	eventually(t, "goblins gate prepush says who holds the turn: "+want, func() bool {
		return strings.Contains(stdout.String(), "goblins gate prepush: waiting for its turn on the machine, ") && strings.Contains(stdout.String(), want)
	})
	time.Sleep(300 * time.Millisecond)
	if checks.Load() != 0 || len(exited) != 0 {
		t.Fatalf("goblins gate prepush ran %d check(s) while a goblins gate turn's command ran; stdout=%q", checks.Load(), stdout.String())
	}
	letGo(t, dir, "held")
	select {
	case exit := <-exited:
		if exit != 0 || checks.Load() != 4 || !strings.HasPrefix(lastLine(stdout.String()), "goblins gate prepush: passed. All 4 checks ran in ") {
			t.Errorf("exit = %d after %d check(s); want 0 after all 4 once the turn was free; stdout=%q stderr=%q", exit, checks.Load(), stdout.String(), stderr.String())
		}
	case <-time.After(time.Minute):
		t.Fatalf("goblins gate prepush had not ended a minute after the turn was free; stdout=%q", stdout.String())
	}
}

// A goblin that puts goblins gate prepush behind goblins gate turn gets one turn: the
// push's checks run inside the turn the wrapper holds, and do not wait for
// it, which they would for good.
func TestGatePrepushBehindGateTurnRunsInsideItsTurn(t *testing.T) {
	// Arrange
	module := prepushModule(t)
	t.Setenv(verify.TurnVariable, "")
	t.Setenv("GOBLINS_TASK_ID", "")

	// Act
	run := startGateTurn(t, module, nil, "--", os.Args[0], gateTurnTestPrepush)
	exit := run.exit(t, "goblins gate prepush behind goblins gate turn")

	// Assert
	printed := run.stdout.String() + run.stderr.String()
	if exit != 0 || strings.Contains(printed, "waiting for its turn") || strings.Contains(printed, "took its turn") {
		t.Errorf("exit = %d and the run printed %q; want 0 with no wait for a turn", exit, printed)
	}
	if got := lastLine(run.stdout.String()); !strings.HasPrefix(got, "goblins gate prepush: passed. All 4 checks ran in ") || strings.Count(run.stdout.String(), "\nran ") != 4 {
		t.Errorf("stdout %q ends with %q; want all 4 checks run", run.stdout.String(), got)
	}
}

// So does a goblins gate turn that the command of another one starts: the inner
// command is told the outer run's turn, and nothing waits.
func TestAGateTurnBehindAGateTurnRunsInsideItsTurn(t *testing.T) {
	// Arrange
	turnLine(t)
	dir := commandFolder(t)

	// Act
	outer := startGateTurn(t, dir, nil, append([]string{"--", os.Args[0], gateTurnTestWrapper}, turnCommand(dir, "inner", 4, false)...)...)
	exit := outer.exit(t, "a goblins gate turn behind a goblins gate turn")

	// Assert
	told, err := os.ReadFile(filepath.Join(dir, "inner.started"))
	if want := fmt.Sprintf("slot-1:%d:", outer.process.Process.Pid); err != nil || !strings.HasPrefix(string(told), want) {
		t.Errorf("the inner command was told the turn %q (%v); want the outer run's, which starts with %q", told, err, want)
	}
	if exit != 4 || outer.stdout.String() != commandOutput("inner") || outer.stderr.String() != commandErrors("inner") {
		t.Errorf("exit = %d, stdout = %q, stderr = %q; want the inner command's 4, %q and %q, with no wait", exit, outer.stdout.String(), outer.stderr.String(), commandOutput("inner"), commandErrors("inner"))
	}
}

// Free memory under the desktop reserve for its ten seconds ends the run's
// command with everything it started, and the run exits 125 with a last line
// that says why, which a gate parks the run on: what the command was ended
// at is no result of its own.
func TestGateTurnEndsItsCommandWhenMemoryStaysUnderTheReserve(t *testing.T) {
	// Arrange
	turnLine(t)
	dir := commandFolder(t)
	marker := filepath.Join(t.TempDir(), "reserve-short")
	run := startGateTurn(t, dir, []string{gateTurnTestMemoryVariable + "=" + marker}, turnCommand(dir, "command", 0, true)...)
	eventually(t, "the run's command starts", func() bool { return left(dir, "command", "started") })
	// The command's own handle, held from its start, names it however long
	// Windows keeps its ended process for anyone holding it open and
	// whatever later takes its pid: a pid that can still be opened is no
	// command that still runs, since a scanner holding the ended process open
	// keeps it openable (issue 1695).
	data, err := os.ReadFile(filepath.Join(dir, "command.pid"))
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil {
		t.Fatal(err)
	}
	command, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(command)

	// Act
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	// Assert
	exit := run.exit(t, "the run, with memory under the reserve,")
	if exit != 125 || left(dir, "command", "ended") {
		t.Fatalf("exit = %d and the command ended by itself = %v; want 125, no result of the command's, with the command ended by the run; stderr=%q", exit, left(dir, "command", "ended"), run.stderr.String())
	}
	lines := strings.Split(strings.TrimSuffix(run.stderr.String(), "\n"), "\n")
	if last := lines[len(lines)-1]; !strings.HasPrefix(last, "goblins gate turn: the run ended its command, since free memory or commit stayed under the desktop reserve: ") || !strings.Contains(last, "reserve pressure") {
		t.Fatalf("last line = %q; want the run saying why it ended its command, which a gate parks the run on", last)
	}
	if event, err := windows.WaitForSingleObject(command, 0); err != nil || event != windows.WAIT_OBJECT_0 {
		t.Fatalf("the command, pid %d, still runs after its run ended it: wait %#x, %v", pid, event, err)
	}
}

// A Ctrl+C at the console reaches the command as it reaches the run, since
// the command shares the run's console group: the command answers it with
// an exit of its own, and the run holds its turn until then and exits with
// it. A command in a group of its own would not hear it at all.
func TestGateTurnsCommandHearsTheConsolesCtrlC(t *testing.T) {
	// Arrange
	slots := turnLine(t)
	dir := commandFolder(t)
	run := startGateTurn(t, dir, nil, "--", os.Args[0], gateTurnTestCommand, dir, "command", "0", "answer-ctrl-c")
	eventually(t, "the run's command starts", func() bool { return left(dir, "command", "started") })

	// Act
	sender := exec.Command(os.Args[0], gateTurnTestCtrlC, strconv.Itoa(run.process.Process.Pid))
	sender.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS}
	if output, err := sender.CombinedOutput(); err != nil {
		t.Fatalf("the console's Ctrl+C was not sent: %v %s", err, output)
	}

	// Assert
	if exit := run.exit(t, "the run, once the console's Ctrl+C reached its command,"); exit != 7 || !left(dir, "command", "interrupted") {
		t.Fatalf("exit = %d and the command heard the Ctrl+C = %v; want the command's own 7; stderr=%q", exit, left(dir, "command", "interrupted"), run.stderr.String())
	}
	if holding, waiting, err := verify.Line(slots); err != nil || len(holding) != 0 || len(waiting) != 0 {
		t.Fatalf("after the run the line holds %+v and %+v (%v); want it empty", holding, waiting, err)
	}
}

// A run given --low-priority lets a run that asked after it go first.
func TestGateTurnAtTheLowestPriorityLetsALaterRunGoFirst(t *testing.T) {
	// Arrange
	slots := turnLine(t)
	dir := commandFolder(t)
	holder, err := verify.Admission{Dir: slots, Available: func() (uint64, error) { return 16 << 30, nil }, Who: "a suite that holds the line", Limit: time.Minute, Poll: 10 * time.Millisecond}.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	low := startGateTurn(t, dir, nil, append([]string{"--low-priority"}, turnCommand(dir, "low", 0, true)...)...)
	eventually(t, "the low-priority run joins the line", func() bool {
		_, waiting, err := verify.Line(slots)
		return err == nil && len(waiting) == 1
	})
	later := startGateTurn(t, dir, nil, turnCommand(dir, "later", 0, true)...)
	eventually(t, "the later run joins the line", func() bool {
		_, waiting, err := verify.Line(slots)
		return err == nil && len(waiting) == 2
	})

	// Act
	if err := holder.Release(); err != nil {
		t.Fatal(err)
	}

	// Assert
	eventually(t, "the later run's command starts", func() bool { return left(dir, "later", "started") })
	if left(dir, "low", "started") {
		t.Error("the low-priority run started while a run that asked after it held the turn")
	}
	letGo(t, dir, "later")
	later.exit(t, "the later run")
	eventually(t, "the low-priority run's command starts", func() bool { return left(dir, "low", "started") })
	letGo(t, dir, "low")
	low.exit(t, "the low-priority run")
}

// A run with no console at all, as a careless start's is once it has left
// the console Windows made for it (leaveOwnConsole), with its standard
// output and error on the null device, still takes its turn and runs its
// command to its end. Nothing here can show a window: a process with no
// console gives its children consoles with none.
func TestGateTurnWithNoConsoleRunsItsCommand(t *testing.T) {
	// Arrange
	turnLine(t)
	dir := commandFolder(t)
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	run := exec.Command(os.Args[0], append([]string{gateTurnTestWrapper}, turnCommand(dir, "command", 4, false)...)...)
	run.Stdin, run.Stdout, run.Stderr = null, null, null
	run.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS}

	// Act
	if err := run.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- run.Wait() }()
	var exit int
	select {
	case <-exited:
		exit = run.ProcessState.ExitCode()
	case <-time.After(time.Minute):
		_ = run.Process.Kill()
		t.Fatal("the run with no console had not ended after a minute")
	}

	// Assert
	if exit != 4 || !left(dir, "command", "ended") {
		t.Fatalf("exit = %d and the command ended = %v; want the command's own 4", exit, left(dir, "command", "ended"))
	}
}

func TestGateTurnExecutionLimitEndsDescendantsAfterTheirParentExits(t *testing.T) {
	// Arrange
	slots := turnLine(t)
	dir := t.TempDir()
	outsideDir := commandFolder(t)
	outside := execx.Command(os.Args[0], gateTurnTestCommand, outsideDir, "outside", "0", "hold")
	if err := outside.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { outside.Process.Kill(); outside.Wait() })
	outsideHandle := standin.Hold(t, outside.Process.Pid)
	args := append([]string{"--execution-limit=8s", "--", os.Args[0]}, treetest.Args(dir, false)...)
	run := startGateTurn(t, dir, nil, args...)
	found, err := treetest.Find(dir)
	defer found.End()
	if err != nil {
		t.Fatal(err)
	}
	if err := treetest.Release(dir); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the root command exits leaving its descendant", func() bool { return !found.Run.IsRunning() })
	if !found.Helper.IsRunning() {
		t.Fatal("the orphaned descendant did not survive its parent")
	}

	// Act
	exit := run.exit(t, "the run with an orphaned descendant")

	// Assert
	if exit != 124 || !strings.Contains(run.stderr.String(), "execution limit of 8s") {
		t.Fatalf("exit = %d, stderr = %q; want a timeout even though the command exited zero", exit, run.stderr.String())
	}
	for _, member := range found.Members() {
		if !member.Ends() {
			t.Errorf("owned process %d survived the timeout", member.PID)
		}
	}
	if event, err := windows.WaitForSingleObject(outsideHandle, 0); err != nil || event != uint32(windows.WAIT_TIMEOUT) {
		t.Fatalf("unrelated process was ended: event=%d, %v", event, err)
	}
	records, err := filepath.Glob(filepath.Join(filepath.Dir(slots), "executions", "*.json"))
	if err != nil || len(records) != 1 {
		t.Fatalf("records = %v, %v", records, err)
	}
	record, err := verify.LoadExecution(records[0])
	if err != nil || record.ExitCode == nil || *record.ExitCode != 0 || record.Status != "failed" || !strings.Contains(record.Failure, verify.ErrLimit.Error()) || !record.Measurement.CleanupConfirmed || record.Measurement.Usage.ActiveProcesses != 0 {
		t.Fatalf("receipt = %+v, %v; want the parent's zero and a failed run with confirmed cleanup", record, err)
	}
}

func TestGateTurnExecutionLimitDoesNotCountAdmission(t *testing.T) {
	// Arrange
	slots := turnLine(t)
	dir := commandFolder(t)
	first := startGateTurn(t, dir, nil, turnCommand(dir, "first", 0, true)...)
	eventually(t, "the first run holds its turn", func() bool { return left(dir, "first", "started") })
	second := startGateTurn(t, dir, nil, append([]string{"--wait=20s", "--execution-limit=3s"}, turnCommand(dir, "second", 0, false)...)...)
	eventually(t, "the bounded run queues", func() bool {
		_, waiting, err := verify.Line(slots)
		return err == nil && len(waiting) == 1
	})

	// Act
	time.Sleep(4 * time.Second)
	second.running(t, "the bounded run waiting longer than its execution limit")
	letGo(t, dir, "first")
	first.exit(t, "the first run")
	exit := second.exit(t, "the bounded run after admission")

	// Assert
	if exit != 0 || !left(dir, "second", "ended") {
		t.Fatalf("exit = %d, stderr = %q; want the command to complete after its independent admission wait", exit, second.stderr.String())
	}
	records, err := filepath.Glob(filepath.Join(filepath.Dir(slots), "executions", "*.json"))
	if err != nil || len(records) != 2 {
		t.Fatalf("records = %v, %v", records, err)
	}
	for _, path := range records {
		record, err := verify.LoadExecution(path)
		if err != nil {
			t.Fatal(err)
		}
		if slices.Contains(record.Command, "second") && (record.Status != "passed" || record.QueueSeconds < 4) {
			t.Fatalf("receipt = %+v; want a pass with admission longer than execution's budget", record)
		}
	}
}

// A run says on its card in the line the memory --memory gave it and what
// its command commits as it runs, which a line of two slots weighs before it
// starts a run beside it.
func TestGateTurnPutsItsMemoryAndWhatItCommitsOnItsCard(t *testing.T) {
	// Arrange
	slots := turnLine(t)
	dir := commandFolder(t)

	// Act
	run := startGateTurn(t, dir, []string{gateTurnTestCommitVariable + "=256"}, append([]string{"--memory", "7GiB"}, turnCommand(dir, "command", 0, true)...)...)
	eventually(t, "the command starts", func() bool { return left(dir, "command", "started") })

	// Assert
	var holding []verify.Standing
	eventually(t, "the run's card says what its command commits", func() bool {
		var err error
		holding, _, err = verify.Line(slots)
		return err == nil && len(holding) == 1 && holding[0].Used >= 256<<20
	})
	if holding[0].Estimate != 7<<30 {
		t.Errorf("the run's card says it is expected to use %d bytes; want the 7 GiB --memory gave it", holding[0].Estimate)
	}
	letGo(t, dir, "command")
	if exit := run.exit(t, "the run"); exit != 0 {
		t.Errorf("exit = %d; want the command's own 0; stderr=%q", exit, run.stderr.String())
	}
}

// A machine service the command started, as Docker Desktop serves the whole
// machine, is left running and named, and the run keeps its command's exit
// code.
func TestGateTurnLeavesAMachineServiceItsCommandStartedRunning(t *testing.T) {
	// Arrange
	turnLine(t)
	programs := t.TempDir()
	standin.RemoveAtCleanup(t, programs)
	// A name of its own, so the test finds its stand-in among every process.
	image := "com.docker.standin-" + strings.ToLower(rand.Text()) + ".exe"
	service := filepath.Join(programs, image)
	standin.Put(t, service)
	for _, variable := range standin.Env("", standin.Rule{Any: true, Hold: true}) {
		name, value, _ := strings.Cut(variable, "=")
		t.Setenv(name, value)
	}
	var stdout, stderr lockedBuffer

	// Act
	exit := gateTurn(turnRuntime(), &stdout, &stderr, "--", "cmd", "/d", "/c", "start", "", "/b", service)

	// Assert
	processes, err := proc.Processes()
	if err != nil {
		t.Fatal(err)
	}
	// Whatever the run did, the stand-in it started ends with the test.
	isRunning := false
	for _, process := range processes {
		if strings.EqualFold(process.ExeBase, image) {
			standin.Hold(t, process.PID)
			isRunning = true
		}
	}
	if exit != 0 || !isRunning {
		t.Fatalf("exit = %d, the service runs = %v; want the command's 0 and the service left running; stderr=%q", exit, isRunning, stderr.String())
	}
	if !strings.Contains(strings.ToLower(stderr.String()), "goblins gate turn: its command started "+image+" pid") || !strings.Contains(stderr.String(), "which serves the whole machine, so the run left it running") {
		t.Fatalf("stderr = %q; want a line naming the service left running", stderr.String())
	}
}

// takeBackCtrlC lets the wrapper hear a Ctrl+C. A program a person starts at
// a console hears its Ctrl+C, while one this test binary starts inherits the
// binary's own way of ignoring it, so the wrapper takes it back for itself
// and what it starts.
func takeBackCtrlC() bool {
	ok, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("SetConsoleCtrlHandler").Call(0, 0)
	return ok != 0
}

// consoleWindow is the console window this process was given, 0 for a
// console with none.
func consoleWindow() uintptr {
	window, _, _ := getConsoleWindow.Call()
	return window
}

// sendConsoleCtrlC is gateTurnTestCtrlC: it sends a Ctrl+C to the console of
// the process pid names.
func sendConsoleCtrlC(pid string) int {
	id, err := strconv.Atoi(pid)
	if err != nil {
		return 2
	}
	kernel := windows.NewLazySystemDLL("kernel32.dll")
	// This process starts with no console, so it can join that one.
	if ok, _, _ := kernel.NewProc("AttachConsole").Call(uintptr(id)); ok == 0 {
		return 3
	}
	if ok, _, _ := kernel.NewProc("SetConsoleCtrlHandler").Call(0, 1); ok == 0 {
		return 4
	}
	if err := windows.GenerateConsoleCtrlEvent(windows.CTRL_C_EVENT, 0); err != nil {
		return 5
	}
	return 0
}
