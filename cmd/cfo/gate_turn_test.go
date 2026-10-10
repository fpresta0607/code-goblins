package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/verify"
)

// The programs this test binary runs as for the tests of cfo gate turn, by
// its first argument. A test binary takes its turns in the store CFO_VERIFY_DIR
// names and in no other, so none of them ever stands in the machine's own
// line: TestGateTurnTakesNoTurnWhereATestNamedNoLineOfItsOwn holds that.
const (
	// gateTurnTestWrapper is cfo gate turn as a process of its own, on a
	// machine whose memory gateTurnTestMemoryVariable sets.
	gateTurnTestWrapper = "gate-turn-test-wrapper"
	// gateTurnTestCommand is a command a turn is taken for.
	gateTurnTestCommand = "gate-turn-test-command"
	// gateTurnTestArguments prints the arguments it was given.
	gateTurnTestArguments = "gate-turn-test-arguments"
	// gateTurnTestGateTest is cfo gate test with stand-ins for go vet and go
	// test, as a command a turn is taken for.
	gateTurnTestGateTest = "gate-turn-test-gate-test"
	// gateTurnTestMemoryVariable names a file. While it exists the wrapper's
	// machine is short of memory: of commit when the file is named
	// commit-short, and of physical memory otherwise.
	gateTurnTestMemoryVariable = "CFO_GATE_TURN_TEST_MEMORY"
)

// runGateTurnTestProgram runs this binary as one of those programs, or says
// that its arguments name none.
func runGateTurnTestProgram() (int, bool) {
	if len(os.Args) < 2 {
		return 0, false
	}
	switch os.Args[1] {
	case gateTurnTestWrapper:
		runtime := defaultCommandRuntime()
		marker := os.Getenv(gateTurnTestMemoryVariable)
		runtime.availableMemory = func() (supervisor.Memory, error) {
			memory := supervisor.Memory{Available: 64 << 30, CommitAvailable: 64 << 30}
			if _, err := os.Stat(marker); err == nil {
				if filepath.Base(marker) == "commit-short" {
					memory.CommitAvailable = 3 << 30
				} else {
					memory.Available = 3 << 30
				}
			}
			return memory, nil
		}
		return runWithRuntime(append([]string{"gate", "turn"}, os.Args[2:]...), os.Stdout, os.Stderr, runtime), true
	case gateTurnTestCommand:
		return runGateTurnTestCommand(os.Args[2:]), true
	case gateTurnTestArguments:
		if err := json.NewEncoder(os.Stdout).Encode(os.Args[2:]); err != nil {
			return 1, true
		}
		return 0, true
	case gateTurnTestGateTest:
		return gateTestWith(standIn(), os.Stdout, os.Stderr, os.Args[2:]...), true
	}
	return 0, false
}

// commandOutput and commandErrors are what the command name writes: bytes a
// wrapper that rewrote lines would change.
func commandOutput(name string) string {
	return "out of " + name + "\r\nthen a bare line feed\nand no end of line"
}

func commandErrors(name string) string {
	return "err of " + name + "\n"
}

// runGateTurnTestCommand is the command name of the folder dir: it leaves
// name.started holding the turn its environment names, leaves name.overlap
// when another command of the folder has started and not ended, writes its
// output, waits for name.release when it was told to hold, leaves name.ended
// and exits with the code it was given.
func runGateTurnTestCommand(args []string) int {
	dir, name := args[0], args[1]
	exit, err := strconv.Atoi(args[2])
	if err != nil {
		return 99
	}
	started := filepath.Join(dir, name+".started")
	if err := os.WriteFile(started, []byte(os.Getenv(verify.TurnVariable)), 0o644); err != nil {
		return 99
	}
	others, _ := filepath.Glob(filepath.Join(dir, "*.started"))
	for _, other := range others {
		if _, err := os.Stat(strings.TrimSuffix(other, ".started") + ".ended"); other != started && err != nil {
			os.WriteFile(filepath.Join(dir, name+".overlap"), []byte(filepath.Base(other)), 0o644)
		}
	}
	os.Stdout.WriteString(commandOutput(name))
	os.Stderr.WriteString(commandErrors(name))
	for len(args) > 3 && args[3] == "hold" {
		if _, err := os.Stat(filepath.Join(dir, name+".release")); err == nil {
			break
		}
		// Its test is over and took the folder with it.
		if _, err := os.Stat(dir); err != nil {
			return 98
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".ended"), nil, 0o644); err != nil {
		return 99
	}
	return exit
}

// turnLine gives the test a line of its own for the turns it takes, and
// returns the folder its slots are in.
func turnLine(t *testing.T) string {
	t.Helper()
	store := t.TempDir()
	t.Setenv("CFO_VERIFY_DIR", store)
	t.Setenv("CFO_VERIFY_SLOTS", "")
	t.Setenv(verify.TurnVariable, "")
	// A goblin's terminal names its task, which is not these runs'.
	t.Setenv("CFO_TASK_ID", "")
	return filepath.Join(store, "slots")
}

// commandFolder is a folder for the marks of a test's commands. A command
// still held when the test ends is let go there and waited for, so none
// outlives its test.
func commandFolder(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Cleanup(func() {
		started, _ := filepath.Glob(filepath.Join(dir, "*.started"))
		for _, mark := range started {
			name := strings.TrimSuffix(filepath.Base(mark), ".started")
			letGo(t, dir, name)
			for deadline := time.Now().Add(10 * time.Second); !left(dir, name, "ended") && time.Now().Before(deadline); {
				time.Sleep(10 * time.Millisecond)
			}
		}
	})
	return dir
}

// turnCommand is the arguments of cfo gate turn that run the command name of
// the folder dir, which exits with exit, and holds until released when hold
// is set.
func turnCommand(dir, name string, exit int, hold bool) []string {
	args := []string{"--", os.Args[0], gateTurnTestCommand, dir, name, strconv.Itoa(exit)}
	if hold {
		args = append(args, "hold")
	}
	return args
}

// left reports whether the command name of dir left the mark.
func left(dir, name, mark string) bool {
	_, err := os.Stat(filepath.Join(dir, name+"."+mark))
	return err == nil
}

// letGo lets the held command name of dir end.
func letGo(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name+".release"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

// eventually waits until ok, for at most a minute: a process starts in well
// under a second, and a loaded machine is the only thing the minute is for.
func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(time.Minute); !ok(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("after a minute, not yet: %s", what)
		}
	}
}

// turnRun is cfo gate turn running as a process of its own.
type turnRun struct {
	process        *exec.Cmd
	stdout, stderr *lockedBuffer
	exited         chan int
}

// startGateTurn starts cfo gate turn in dir with args, as a process of its
// own that env is added to the environment of. A run still going when the
// test ends is ended there.
func startGateTurn(t *testing.T, dir string, env []string, args ...string) *turnRun {
	t.Helper()
	run := &turnRun{process: exec.Command(os.Args[0], append([]string{gateTurnTestWrapper}, args...)...), stdout: &lockedBuffer{}, stderr: &lockedBuffer{}, exited: make(chan int, 1)}
	run.process.Dir, run.process.Env = dir, append(os.Environ(), env...)
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

// gateTurn runs cfo gate turn in this process, on a machine with memory to
// spare.
func gateTurn(runtime commandRuntime, stdout, stderr *lockedBuffer, args ...string) int {
	return runWithRuntime(append([]string{"gate", "turn"}, args...), stdout, stderr, runtime)
}

// turnRuntime is the runtime of a machine with memory to spare.
func turnRuntime() commandRuntime {
	runtime := defaultCommandRuntime()
	runtime.availableMemory = plenty
	return runtime
}

// A test binary takes turns only in a line its test named. With none named,
// cfo gate turn takes no turn and starts nothing, so no test of this suite
// can stand in the machine's own line or be held back by it.
func TestGateTurnTakesNoTurnWhereATestNamedNoLineOfItsOwn(t *testing.T) {
	// Arrange
	turnLine(t)
	t.Setenv("CFO_VERIFY_DIR", "")
	dir := commandFolder(t)
	var stdout, stderr lockedBuffer

	// Act
	exit := gateTurn(turnRuntime(), &stdout, &stderr, turnCommand(dir, "command", 0, false)...)

	// Assert
	if exit != 125 || left(dir, "command", "started") || stdout.String() != "" {
		t.Errorf("exit = %d, the command started = %v, stdout = %q; want 125 with nothing started and nothing printed", exit, left(dir, "command", "started"), stdout.String())
	}
	if want := "cfo gate turn: this run takes no turn: verify: a test must name its own report store in CFO_VERIFY_DIR"; !strings.HasPrefix(stderr.String(), want) {
		t.Errorf("stderr = %q; want it to start with %q", stderr.String(), want)
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

// Everything after -- is the command's, unparsed: its own flags, a -- of its
// own, an empty argument, spaces, quotes and what a shell would read as
// operators all reach it as they were given.
func TestGateTurnGivesTheCommandItsArgumentsAsTheyWere(t *testing.T) {
	// Arrange
	turnLine(t)
	given := []string{"--as", "not the wrapper's", "--", "-run", "Test A|B", "", `say "hello"`, "a && b", "%PATH%", `C:\with space\`}
	var stdout, stderr lockedBuffer

	// Act
	exit := gateTurn(turnRuntime(), &stdout, &stderr, append([]string{"--as", "a named run", "--", os.Args[0], gateTurnTestArguments}, given...)...)

	// Assert
	var got []string
	if err := json.Unmarshal([]byte(stdout.String()), &got); exit != 0 || err != nil || fmt.Sprint(got) != fmt.Sprint(given) || len(got) != len(given) {
		t.Errorf("exit = %d, the command was given %q (%v); want %q; stderr=%q", exit, got, err, given, stderr.String())
	}
}

// One heavy run at a time: a second cfo gate turn started while the first
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
	if waiting := second.stderr.String(); !strings.HasPrefix(waiting, "cfo gate turn: waiting for its turn (") || !strings.Contains(waiting, "; this run is next in line\n") || strings.Contains(waiting, " budget") {
		t.Errorf("while it waited the second run said %q; want lines that start with cfo gate turn: and say where it stands in line, and no budget for a holder that has none", waiting)
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
	took := strings.Index(said, "cfo gate turn: took its turn after ")
	if took < 0 || !strings.HasSuffix(said, "s\n"+commandErrors("second")) || strings.Count(said, "cfo gate turn: took its turn after ") != 1 {
		t.Errorf("the second run's stderr = %q; want one line saying it took its turn, then its command's %q and nothing after", said, commandErrors("second"))
	}
	if holding, waiting, err := verify.Line(filepath.Join(os.Getenv("CFO_VERIFY_DIR"), "slots")); err != nil || len(holding) != 0 || len(waiting) != 0 {
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
	// The first run's command outlives the run that started it, as a command
	// does when only its wrapper is ended, until the test's end lets it go.
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

// The wrapper guesses no limit. cfo gate test gives a run at the head of the
// line an hour of memory under the floor and then refuses it; behind cfo gate
// turn a run waits for as long as memory is short, and the limits that end it
// are its command's own and its caller's.
func TestGateTurnWaitsForMemoryPastTheLimitOfCfoGateTest(t *testing.T) {
	// Arrange
	turnLine(t)
	dir := commandFolder(t)
	var isShort atomic.Bool
	isShort.Store(true)
	runtime := turnRuntime()
	runtime.gateWaitLimit = 20 * time.Millisecond
	runtime.availableMemory = func() (supervisor.Memory, error) {
		if isShort.Load() {
			return supervisor.Memory{Available: 64 << 30, CommitAvailable: 1 << 30}, nil
		}
		return plenty()
	}
	var stdout, stderr lockedBuffer
	exited := make(chan int, 1)

	// Act
	go func() { exited <- gateTurn(runtime, &stdout, &stderr, turnCommand(dir, "command", 6, false)...) }()

	// Assert
	eventually(t, "the run says it waits for memory", func() bool { return strings.Contains(stderr.String(), "1.0 GB of memory is available") })
	time.Sleep(500 * time.Millisecond)
	select {
	case exit := <-exited:
		t.Fatalf("the run gave up with exit %d after memory was short for half a second; stderr=%q", exit, stderr.String())
	default:
	}
	isShort.Store(false)
	select {
	case exit := <-exited:
		if exit != 6 || !left(dir, "command", "ended") {
			t.Errorf("exit = %d; want the command's 6 once the machine had the memory; stderr=%q", exit, stderr.String())
		}
	case <-time.After(time.Minute):
		t.Fatalf("the run had not ended a minute after the machine had the memory; stderr=%q", stderr.String())
	}
}

// cfo gate test and cfo gate turn stand in one line. A cfo gate test started
// while a cfo gate turn's command runs names that run and starts no check
// until the command has ended.
func TestGateTestWaitsForTheTurnOfAGateTurn(t *testing.T) {
	// Arrange
	module := testStepModule(t, nil, map[string]string{"a/a.go": "package a\n\nfunc A() int { return 2 }\n"})
	t.Chdir(module)
	t.Setenv(verify.TurnVariable, "")
	t.Setenv("CFO_TASK_ID", "")
	dir := commandFolder(t)
	holder := startGateTurn(t, dir, nil, append([]string{"--as", "another repository's tests"}, turnCommand(dir, "held", 0, true)...)...)
	eventually(t, "the holder's command starts", func() bool { return left(dir, "held", "started") })

	// Act
	var stdout, stderr lockedBuffer
	exited := make(chan int, 1)
	go func() { exited <- gateTestWith(standIn(), &stdout, &stderr) }()

	// Assert
	want := fmt.Sprintf("the turn is held by another repository's tests: %s (pid %d), for ", strings.Join(turnCommand(dir, "held", 0, true)[1:], " "), holder.process.Process.Pid)
	eventually(t, "cfo gate test says who holds the turn: "+want, func() bool {
		return strings.Contains(stdout.String(), "cfo gate test: waiting for its turn (") && strings.Contains(stdout.String(), want)
	})
	time.Sleep(300 * time.Millisecond)
	if strings.Contains(stdout.String(), "ran go ") || len(exited) != 0 {
		t.Fatalf("cfo gate test went on while a cfo gate turn's command ran; stdout=%q", stdout.String())
	}
	letGo(t, dir, "held")
	select {
	case exit := <-exited:
		if exit != 0 || !strings.Contains(stdout.String(), "ran go test") {
			t.Errorf("exit = %d; want 0 with the tests run once the turn was free; stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
		}
	case <-time.After(time.Minute):
		t.Fatalf("cfo gate test had not ended a minute after the turn was free; stdout=%q", stdout.String())
	}
}

// And the other way round: a cfo gate turn started while a cfo gate test runs
// its checks names that run, with its budget, and starts its command only
// once the checks have ended.
func TestGateTurnWaitsForTheTurnOfAGateTest(t *testing.T) {
	// Arrange
	module := testStepModule(t, nil, map[string]string{"a/a.go": "package a\n\nfunc A() int { return 2 }\n"})
	t.Chdir(module)
	t.Setenv(verify.TurnVariable, "")
	t.Setenv("CFO_TASK_ID", "")
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
		t.Fatalf("cfo gate test had not started its tests after a minute; stdout=%q stderr=%q", stdout.String(), stderr.String())
	}

	// Act
	run := startGateTurn(t, dir, nil, turnCommand(dir, "command", 2, false)...)

	// Assert
	held := fmt.Sprintf(" (pid %d), for ", os.Getpid())
	eventually(t, "the run says cfo gate test holds the turn", func() bool {
		said := run.stderr.String()
		return strings.Contains(said, "cfo gate turn: waiting for its turn (") && strings.Contains(said, "the turn is held by m at ") && strings.Contains(said, held) && strings.Contains(said, " of its 1h30m0s budget; this run is next in line\n")
	})
	time.Sleep(300 * time.Millisecond)
	run.running(t, "the run, while cfo gate test ran its tests,")
	if left(dir, "command", "started") {
		t.Fatal("the run's command started while cfo gate test ran its tests")
	}
	finished()
	if exit := run.exit(t, "the run, once cfo gate test had ended,"); exit != 2 || !left(dir, "command", "ended") {
		t.Errorf("the run's exit = %d; want its command's 2; stderr=%q", exit, run.stderr.String())
	}
	select {
	case exit := <-exited:
		if exit != 0 {
			t.Errorf("cfo gate test's exit = %d; want 0; stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
		}
	case <-time.After(time.Minute):
		t.Fatalf("cfo gate test had not ended a minute after its tests; stdout=%q", stdout.String())
	}
}

// This repository's own test command is cfo gate test, which takes a turn
// for each of its checks. Behind cfo gate turn it runs inside the turn taken
// for it, and does not wait for that turn, which it would wait on for good.
func TestGateTestBehindGateTurnRunsInsideItsTurn(t *testing.T) {
	// Arrange
	module := testStepModule(t, nil, map[string]string{"a/a.go": "package a\n\nfunc A() int { return 2 }\n"})
	t.Setenv(verify.TurnVariable, "")
	t.Setenv("CFO_TASK_ID", "")

	// Act
	run := startGateTurn(t, module, nil, "--", os.Args[0], gateTurnTestGateTest)
	exit := run.exit(t, "cfo gate test behind cfo gate turn")

	// Assert
	printed := run.stdout.String() + run.stderr.String()
	if exit != 0 || strings.Contains(printed, "waiting for its turn") || strings.Contains(printed, "took its turn") {
		t.Errorf("exit = %d and the run printed %q; want 0 with no wait for a turn", exit, printed)
	}
	for _, want := range []string{"ran go vet\n", "ran go test\n", "cfo gate test: passed at level fast in "} {
		if !strings.Contains(run.stdout.String(), want) {
			t.Errorf("stdout %q lacks %q", run.stdout.String(), want)
		}
	}
}

// So does a cfo gate turn that the command of another one starts: the inner
// command is told the outer run's turn, and nothing waits.
func TestAGateTurnBehindAGateTurnRunsInsideItsTurn(t *testing.T) {
	// Arrange
	turnLine(t)
	dir := commandFolder(t)

	// Act
	outer := startGateTurn(t, dir, nil, append([]string{"--", os.Args[0], gateTurnTestWrapper}, turnCommand(dir, "inner", 4, false)...)...)
	exit := outer.exit(t, "a cfo gate turn behind a cfo gate turn")

	// Assert
	told, err := os.ReadFile(filepath.Join(dir, "inner.started"))
	if want := fmt.Sprintf("slot-1:%d:", outer.process.Process.Pid); err != nil || !strings.HasPrefix(string(told), want) {
		t.Errorf("the inner command was told the turn %q (%v); want the outer run's, which starts with %q", told, err, want)
	}
	if exit != 4 || outer.stdout.String() != commandOutput("inner") || outer.stderr.String() != commandErrors("inner") {
		t.Errorf("exit = %d, stdout = %q, stderr = %q; want the inner command's 4, %q and %q, with no wait", exit, outer.stdout.String(), outer.stderr.String(), commandOutput("inner"), commandErrors("inner"))
	}
}

// Interrupted while it waits, a run leaves the line and starts nothing. It
// exits 125, which says no turn was taken, and the run that holds the turn
// keeps it.
func TestGateTurnInterruptedWhileItWaitsLeavesTheLineAndStartsNothing(t *testing.T) {
	// Arrange
	slots := turnLine(t)
	dir := commandFolder(t)
	holdTheTurn(t)
	signals := make(chan os.Signal, 1)
	runtime := turnRuntime()
	runtime.interrupts = func() (<-chan os.Signal, func()) { return signals, func() {} }
	var stdout, stderr lockedBuffer
	exited := make(chan int, 1)
	go func() { exited <- gateTurn(runtime, &stdout, &stderr, turnCommand(dir, "command", 0, false)...) }()
	eventually(t, "the run waits in line", func() bool {
		_, waiting, _ := verify.Line(slots)
		return len(waiting) == 1 && strings.Contains(stderr.String(), "cfo gate turn: waiting for its turn (")
	})

	// Act
	signals <- os.Interrupt

	// Assert
	select {
	case exit := <-exited:
		if exit != 125 || left(dir, "command", "started") || stdout.String() != "" {
			t.Errorf("exit = %d, the command started = %v, stdout = %q; want 125 with nothing started and nothing printed", exit, left(dir, "command", "started"), stdout.String())
		}
	case <-time.After(time.Minute):
		t.Fatalf("the run had not ended a minute after it was interrupted; stderr=%q", stderr.String())
	}
	if want := "cfo gate turn: this run takes no turn: it was interrupted while it waited\n"; !strings.HasSuffix(stderr.String(), want) {
		t.Errorf("stderr = %q; want it to end with %q", stderr.String(), want)
	}
	holding, waiting, err := verify.Line(slots)
	if err != nil || len(holding) != 1 || holding[0].Who != "another run" || len(waiting) != 0 {
		t.Errorf("the line holds %+v and %+v (%v); want the other run still holding its turn and no run waiting", holding, waiting, err)
	}
}

// Interrupted while its command runs, a run keeps its turn until the command
// has ended, and exits with the command's code: the command shares the
// console and is sent the same signal, and what it does about it is its own.
func TestGateTurnInterruptedWhileItsCommandRunsKeepsItsTurnUntilTheCommandEnds(t *testing.T) {
	// Arrange
	slots := turnLine(t)
	dir := commandFolder(t)
	signals := make(chan os.Signal, 1)
	runtime := turnRuntime()
	runtime.interrupts = func() (<-chan os.Signal, func()) { return signals, func() {} }
	var stdout, stderr lockedBuffer
	exited := make(chan int, 1)
	go func() { exited <- gateTurn(runtime, &stdout, &stderr, turnCommand(dir, "command", 9, true)...) }()
	eventually(t, "the run's command starts", func() bool { return left(dir, "command", "started") })

	// Act
	signals <- os.Interrupt

	// Assert
	time.Sleep(300 * time.Millisecond)
	holding, _, err := verify.Line(slots)
	if len(exited) != 0 || err != nil || len(holding) != 1 || holding[0].PID != os.Getpid() || left(dir, "command", "ended") {
		t.Fatalf("after the interrupt the run had ended = %v and the line held %+v (%v); want the run still holding its turn while its command runs", len(exited) != 0, holding, err)
	}
	letGo(t, dir, "command")
	select {
	case exit := <-exited:
		if exit != 9 {
			t.Errorf("exit = %d; want the command's 9; stderr=%q", exit, stderr.String())
		}
	case <-time.After(time.Minute):
		t.Fatalf("the run had not ended a minute after its command; stderr=%q", stderr.String())
	}
	if holding, waiting, err := verify.Line(slots); err != nil || len(holding) != 0 || len(waiting) != 0 {
		t.Errorf("after the run the line holds %+v and %+v (%v); want it empty", holding, waiting, err)
	}
}

// A run that holds the turn is named to the runs behind it, and to cfo gate
// turns: by the name its caller gave it or by its folder, with its command,
// and with the fleet task and the goblin whose run it is. It names no budget,
// since it has none. Under a gate the task in the environment is the gate
// daemon's and not the run's, so it is not named.
func TestGateTurnNamesItsRunToTheRunsBehindIt(t *testing.T) {
	for name, test := range map[string]struct {
		as, task, goblin, gate string
	}{
		"named by its caller":                     {as: "PrecisionDocs feat/x, run 01M4FG7F"},
		"not named":                               {},
		"a goblin's own run":                      {task: "cg-example", goblin: "Zora"},
		"a goblin's run, named by its caller":     {as: "the whole suite", task: "cg-example", goblin: "Zora"},
		"a task whose record names no goblin":     {task: "cg-example"},
		"under a gate, whose task is not its own": {task: "cg-misleading", goblin: "Zora", gate: "1"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			slots := turnLine(t)
			dir := commandFolder(t)
			t.Chdir(dir)
			folder, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			home := t.TempDir()
			t.Setenv("CFO_HOME", home)
			t.Setenv("CFO_STATE_OVERRIDE", "")
			t.Setenv("CFO_TASK_ID", test.task)
			t.Setenv("NO_MISTAKES_GATE", test.gate)
			if err := os.MkdirAll(filepath.Join(home, "state"), 0o755); err != nil {
				t.Fatal(err)
			}
			if test.task != "" {
				if err := state.WriteTaskMeta(filepath.Join(home, "state"), state.TaskMeta{ID: test.task, Project: dir, Worktree: dir, GoblinName: test.goblin}); err != nil {
					t.Fatal(err)
				}
			}
			args := turnCommand(dir, "command", 0, true)
			want := strings.Join(args[1:], " ")
			if test.as != "" {
				want = test.as + ": " + want
				args = append([]string{"--as", test.as}, args...)
			} else {
				want += " in " + folder
			}
			switch {
			case test.gate != "" || test.task == "":
			case test.goblin != "":
				want += ", task " + test.goblin + " (" + test.task + ")"
			default:
				want += ", task " + test.task
			}
			var stdout, stderr lockedBuffer
			exited := make(chan int, 1)

			// Act
			go func() { exited <- gateTurn(turnRuntime(), &stdout, &stderr, args...) }()
			eventually(t, "the run's command starts", func() bool { return left(dir, "command", "started") })
			holding, _, lineErr := verify.Line(slots)
			var turns, turnsErr bytes.Buffer
			turnsExit := gateTurns(plenty, &turns, &turnsErr)
			letGo(t, dir, "command")

			// Assert
			if lineErr != nil || len(holding) != 1 || holding[0].Who != want || holding[0].Budget != 0 || holding[0].PID != os.Getpid() {
				t.Errorf("the line held %+v (%v); want this process holding the turn as %q under no budget", holding, lineErr, want)
			}
			if line := fmt.Sprintf("turn: %s (pid %d), for ", want, os.Getpid()); turnsExit != 0 || !strings.HasPrefix(turns.String(), line) || strings.Contains(turns.String(), "budget") {
				t.Errorf("cfo gate turns exited %d and printed %q; want a line that starts %q and names no budget", turnsExit, turns.String(), line)
			}
			select {
			case exit := <-exited:
				if exit != 0 || stderr.String() != commandErrors("command") {
					t.Errorf("exit = %d and stderr = %q; want 0 and the command's own %q", exit, stderr.String(), commandErrors("command"))
				}
			case <-time.After(time.Minute):
				t.Fatalf("the run had not ended a minute after its command; stderr=%q", stderr.String())
			}
		})
	}
}

// cfo gate test names the goblin whose run it is beside its task too, and
// its report keeps the task's id.
func TestGateTestNamesTheGoblinWhoseRunItIs(t *testing.T) {
	// Arrange
	module := testStepModule(t, nil, map[string]string{"a/a.go": "package a\n\nfunc A() int { return 2 }\n"})
	t.Chdir(module)
	home := t.TempDir()
	t.Setenv("CFO_HOME", home)
	t.Setenv("CFO_STATE_OVERRIDE", "")
	t.Setenv("CFO_TASK_ID", "cg-example")
	t.Setenv("NO_MISTAKES_GATE", "")
	if err := os.MkdirAll(filepath.Join(home, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteTaskMeta(filepath.Join(home, "state"), state.TaskMeta{ID: "cg-example", Project: module, Worktree: module, GoblinName: "Zora"}); err != nil {
		t.Fatal(err)
	}
	var turns bytes.Buffer
	runtime := standIn()
	runtime.gateRun = func(command []string, _ string, _ []string, _, stderr io.Writer) (int, error) {
		if command[1] == "test" {
			if exit := runGateTurns(&turns, stderr, plenty); exit != 0 {
				t.Errorf("turns exited %d", exit)
			}
		}
		return 0, nil
	}
	var stdout, stderr bytes.Buffer

	// Act
	exit := gateTestWith(runtime, &stdout, &stderr)

	// Assert
	report, _ := lastReport(t)
	if exit != 0 || report.Task != "cg-example" || !strings.Contains(turns.String(), ", task Zora (cg-example) (pid ") {
		t.Errorf("exit = %d, the report's task = %q, cfo gate turns printed %q; want 0, the task's id, and the run named as the task of Zora (cg-example); stderr=%q", exit, report.Task, turns.String(), stderr.String())
	}
}

// A run given arguments that name no command takes no turn and starts
// nothing: it exits 125 and says how it is called.
func TestGateTurnTakesNoTurnWithoutACommand(t *testing.T) {
	for name, args := range map[string][]string{
		"no arguments":             nil,
		"nothing after --":         {"--"},
		"a name and nothing after": {"--as", "a named run", "--"},
		"a command with no --":     {os.Args[0], gateTurnTestArguments},
		"a name with no --":        {"--as", "a named run", os.Args[0], gateTurnTestArguments},
		"a name that is missing":   {"--as"},
		"an argument of no kind":   {"--level", "fast", "--", os.Args[0], gateTurnTestArguments},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			slots := turnLine(t)
			var stdout, stderr lockedBuffer

			// Act
			exit := gateTurn(turnRuntime(), &stdout, &stderr, args...)

			// Assert
			if exit != 125 || stdout.String() != "" {
				t.Errorf("exit = %d and stdout = %q; want 125 and nothing from a command", exit, stdout.String())
			}
			lines := strings.Split(strings.TrimSuffix(stderr.String(), "\n"), "\n")
			if len(lines) != 2 || lines[0] != "usage: cfo gate turn [--as <name>] -- <program> [<argument>...]" || !strings.HasPrefix(lines[1], "cfo gate turn: this run takes no turn: ") {
				t.Errorf("stderr = %q; want the usage, then a last line that starts cfo gate turn: this run takes no turn: ", stderr.String())
			}
			if _, err := os.Stat(slots); !os.IsNotExist(err) {
				t.Errorf("the line's folder %s: %v; want none made by a run that took no turn", slots, err)
			}
		})
	}
}

// A command that cannot be started is said so, with exit 126, and the turn
// taken for it is given back.
func TestGateTurnSaysWhenItsCommandDidNotStart(t *testing.T) {
	// Arrange
	slots := turnLine(t)
	missing := filepath.Join(t.TempDir(), "no-such-program.exe")
	var stdout, stderr lockedBuffer

	// Act
	exit := gateTurn(turnRuntime(), &stdout, &stderr, "--", missing, "an argument")

	// Assert
	if want := "cfo gate turn: the command did not start: "; exit != 126 || !strings.HasPrefix(stderr.String(), want) || strings.Count(stderr.String(), "\n") != 1 || stdout.String() != "" {
		t.Errorf("exit = %d, stderr = %q, stdout = %q; want 126 and one line that starts %q", exit, stderr.String(), stdout.String(), want)
	}
	if holding, waiting, err := verify.Line(slots); err != nil || len(holding) != 0 || len(waiting) != 0 {
		t.Errorf("the line holds %+v and %+v (%v); want the turn given back", holding, waiting, err)
	}
}
