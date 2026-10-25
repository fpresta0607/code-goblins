package verify

import (
	"bufio"
	"context"
	"errors"
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

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/standin"
)

// limitsRole names what this test binary is when an executor limits test
// starts it: a command that leaves a child running, that child, or a
// command that outruns its limit.
const limitsRole = "VERIFY_LIMITS_ROLE"

func plentyOfMemory() (Headroom, error) {
	return Headroom{PhysicalAvailable: 8 << 30, CommitAvailable: 8 << 30}, nil
}

func TestExecutorEndsAndNamesWhatTheCommandLeftRunning(t *testing.T) {
	switch os.Getenv(limitsRole) {
	case "parent":
		child := execx.Command(os.Args[0], os.Args[1:]...)
		child.Env = append(os.Environ(), limitsRole+"=child")
		if err := child.Start(); err != nil {
			os.Exit(8)
		}
		for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if data, err := os.ReadFile("child.pid"); err == nil && len(data) > 0 {
				os.Exit(0)
			}
		}
		os.Exit(7)
	case "child":
		if err := os.WriteFile("child.pid", []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			os.Exit(6)
		}
		time.Sleep(time.Minute)
		os.Exit(9)
	}
	// Arrange
	store := t.TempDir()
	t.Setenv("CFO_VERIFY_DIR", store)
	root := t.TempDir()
	executor := Executor{Admission: admission(t), Monitor: Monitor{Poll: 10 * time.Millisecond, Linger: 200 * time.Millisecond, Headroom: plentyOfMemory}}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Act
	result, record, err := executor.RunWithin(ctx, execx.Request{Dir: root, Name: os.Args[0], Args: []string{"-test.run=^TestExecutorEndsAndNamesWhatTheCommandLeftRunning$"}, Env: append(os.Environ(), limitsRole+"=parent")}, 0)

	// Assert
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("a command that exited 0 and left a child = %+v, %v, want its own result", result, err)
	}
	childPID, err := strconv.Atoi(strings.TrimSpace(string(waitForFile(t, filepath.Join(root, "child.pid"), time.Minute))))
	if err != nil {
		t.Fatal(err)
	}
	if child, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(childPID)); err == nil {
		isEnded := endsSoon(child)
		windows.CloseHandle(child)
		if !isEnded {
			t.Fatal("the child the command left still runs after the run ended")
		}
	}
	evidence := readExecution(t, record)
	leftovers := evidence.Measurement.Leftovers
	if evidence.Status != "passed" || len(leftovers) != 1 || leftovers[0].PID != childPID || !strings.EqualFold(leftovers[0].Name, filepath.Base(os.Args[0])) {
		t.Fatalf("evidence = %+v, want a pass naming child pid %d as left running", evidence, childPID)
	}
}

func TestExecutorLimitCountsFromTheCommandsStartNotItsWait(t *testing.T) {
	// Arrange
	store := t.TempDir()
	t.Setenv("CFO_VERIFY_DIR", store)
	root := t.TempDir()
	standin.RemoveAtCleanup(t, root)
	program := filepath.Join(root, "workload.exe")
	standin.Put(t, program)
	// A program's first run on this machine waits for its scan, which a
	// loaded machine stretched past a three second limit on 2026-10-14, so
	// the program has run once before its run is timed.
	warm := exec.Command(program)
	warm.Env = append(os.Environ(), standin.Env("", standin.Rule{Any: true, Exit: 0})...)
	if err := warm.Run(); err != nil {
		t.Fatal(err)
	}
	executor := Executor{Admission: admission(t), Monitor: Monitor{Poll: 10 * time.Millisecond, Headroom: plentyOfMemory}}
	holder := exec.Command(os.Args[0])
	holder.Env = append(os.Environ(), "VERIFY_TEST_HOLD_DIR="+executor.Admission.Dir)
	release, err := holder.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	said, err := holder.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { release.Close(); holder.Wait() })
	if line, err := bufio.NewReader(said).ReadString('\n'); err != nil || strings.TrimSpace(line) != "holding" {
		t.Fatalf("the holder said %q, %v", line, err)
	}
	// The wait outlasts the limit, which has room for a loaded machine.
	go func() {
		time.Sleep(12 * time.Second)
		release.Close()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Act
	result, record, err := executor.RunWithin(ctx, execx.Request{Dir: root, Name: program, Env: append(os.Environ(), standin.Env("", standin.Rule{Any: true, Stdout: "ran", Exit: 0})...)}, 10*time.Second)

	// Assert
	if err != nil || result.ExitCode != 0 || string(result.Stdout) != "ran" {
		t.Fatalf("a quick command behind a longer wait = %+v, %v", result, err)
	}
	if evidence := readExecution(t, record); evidence.Status != "passed" || evidence.QueueSeconds < 11 {
		t.Fatalf("evidence = %+v, want a pass after a wait longer than the limit", evidence)
	}
}

// A run's wait is the whole time it waited for its turn, the setup it makes
// before it joins the line included. A loaded machine stretched that setup
// past a second on 2026-10-23, and the record said the run had waited a
// hundredth of a second.
func TestExecutorCountsTheSetupBeforeItsTurnInTheWait(t *testing.T) {
	// Arrange
	store := t.TempDir()
	t.Setenv("CFO_VERIFY_DIR", store)
	root := t.TempDir()
	standin.RemoveAtCleanup(t, root)
	program := filepath.Join(root, "workload.exe")
	standin.Put(t, program)
	executor := Executor{Admission: admission(t), Monitor: Monitor{Poll: 10 * time.Millisecond, Headroom: plentyOfMemory}}
	// A run takes the line's custody before it joins the line, so holding
	// that custody stretches exactly the part of the wait the setup is.
	unlock, err := custodyMutation(executor.Admission.Dir)
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	var heldUntil time.Time
	release := func() {
		once.Do(func() {
			heldUntil = time.Now()
			_ = unlock()
		})
	}
	t.Cleanup(release)
	go func() {
		time.Sleep(2 * time.Second)
		release()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	// Act
	asked := time.Now()
	result, record, err := executor.RunWithin(ctx, execx.Request{Dir: root, Name: program, Env: append(os.Environ(), standin.Env("", standin.Rule{Any: true, Stdout: "ran", Exit: 0})...)}, 0)

	// Assert
	if err != nil || result.ExitCode != 0 || string(result.Stdout) != "ran" {
		t.Fatalf("a quick command behind a slow setup = %+v, %v", result, err)
	}
	// The run takes its turn only once the custody it waits for is given
	// back, so its wait is at least the time from the ask to that moment.
	evidence := readExecution(t, record)
	waitedAtLeast := heldUntil.Sub(asked)
	if evidence.Status != "passed" || evidence.QueueSeconds < waitedAtLeast.Seconds() {
		t.Fatalf("evidence = %+v, want a pass saying it waited the %s its setup held it", evidence, waitedAtLeast)
	}
}

func TestExecutorLimitEndsACommandThatOutrunsIt(t *testing.T) {
	if os.Getenv(limitsRole) == "sleeper" {
		time.Sleep(time.Minute)
		os.Exit(9)
	}
	// Arrange
	store := t.TempDir()
	t.Setenv("CFO_VERIFY_DIR", store)
	executor := Executor{Admission: admission(t), Monitor: Monitor{Poll: 10 * time.Millisecond, Grace: 10 * time.Millisecond, Headroom: plentyOfMemory}}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	started := time.Now()

	// Act
	_, record, err := executor.RunWithin(ctx, execx.Request{Dir: t.TempDir(), Name: os.Args[0], Args: []string{"-test.run=^TestExecutorLimitEndsACommandThatOutrunsIt$"}, Env: append(os.Environ(), limitsRole+"=sleeper")}, 300*time.Millisecond)

	// Assert
	if !errors.Is(err, ErrLimit) || time.Since(started) > 30*time.Second {
		t.Fatalf("a command past its limit = %v after %s", err, time.Since(started))
	}
	evidence := readExecution(t, record)
	if evidence.Status != "failed" || evidence.Measurement.State != Interrupted || !evidence.Measurement.CleanupConfirmed || evidence.Measurement.Usage.ActiveProcesses != 0 {
		t.Fatalf("evidence = %+v", evidence)
	}
}

func TestExecutorRefusesAnUntrustedDesktopReserveBeforeStarting(t *testing.T) {
	// Arrange
	store := t.TempDir()
	t.Setenv("CFO_VERIFY_DIR", store)
	root := t.TempDir()
	standin.RemoveAtCleanup(t, root)
	program := filepath.Join(root, "workload.exe")
	standin.Put(t, program)
	executor := Executor{Admission: admission(t), Monitor: Monitor{Poll: 10 * time.Millisecond, Headroom: plentyOfMemory}}
	if err := os.WriteFile(filepath.Join(executor.Admission.Dir, "reserve.json"), []byte(`{"physical_gib": 0, "commit_gib": 2}`), 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	result, record, err := executor.RunWithin(context.Background(), execx.Request{Dir: root, Name: program, Env: append(os.Environ(), standin.Env("", standin.Rule{Any: true, Stdout: "ran", Exit: 0})...)}, 0)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "reserve.json") || len(result.Stdout) != 0 {
		t.Fatalf("an untrusted reserve ran the command: %+v, %v", result, err)
	}
	if evidence := readExecution(t, record); evidence.Status != "not_started" || evidence.ExitCode != nil {
		t.Fatalf("evidence = %+v, want a run that never started", evidence)
	}
	if _, err := os.Stat(filepath.Join(executor.Admission.Dir, "slot-1")); !os.IsNotExist(err) {
		t.Fatalf("the refused run kept its turn: %v", err)
	}
}

// A run is asked whether it still needs its command once it holds its turn,
// and a run that does not gives the turn back with the command never started
// and why recorded.
func TestExecutorStartsTheCommandOnlyWhenItsRunStillNeedsIt(t *testing.T) {
	for name, row := range map[string]struct {
		ready     error
		isStarted bool
	}{
		"a run that needs it":       {isStarted: true},
		"a run whose work was done": {ready: errors.New("a run ahead did this work")},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			t.Setenv("CFO_VERIFY_DIR", t.TempDir())
			root := t.TempDir()
			executor := Executor{Admission: admission(t), Monitor: Monitor{Poll: 10 * time.Millisecond, Headroom: plentyOfMemory}}
			asked, isHeldWhenAsked := 0, false
			executor.Ready = func(context.Context) error {
				asked++
				_, err := os.Stat(filepath.Join(executor.Admission.Dir, "slot-1"))
				isHeldWhenAsked = err == nil
				return row.ready
			}
			sourceReads := 0
			executor.Source = func(context.Context, string) (Source, error) {
				sourceReads++
				return Source{Commit: "c1", Tree: "t1", IsComplete: true}, nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			// Act
			_, record, err := executor.RunWithin(ctx, execx.Request{Dir: root, Name: "cmd", Args: []string{"/d", "/c", "echo ran> ran.txt"}}, 0)

			// Assert
			if asked != 1 || !isHeldWhenAsked {
				t.Fatalf("the run was asked %d time(s), with its turn held %v; want once, with it held", asked, isHeldWhenAsked)
			}
			// A run that does not need its command reads no source for it.
			if wantReads := map[bool]int{true: 2, false: 0}[row.isStarted]; sourceReads != wantReads {
				t.Fatalf("the source was read %d time(s), want %d", sourceReads, wantReads)
			}
			_, statErr := os.Stat(filepath.Join(root, "ran.txt"))
			if isStarted := statErr == nil; isStarted != row.isStarted {
				t.Fatalf("the command started = %v, want %v", isStarted, row.isStarted)
			}
			evidence := readExecution(t, record)
			if row.isStarted {
				if err != nil || evidence.Status != "passed" {
					t.Fatalf("a run that needs its command = %v, evidence %+v; want a pass", err, evidence)
				}
				return
			}
			if !errors.Is(err, ErrNotStarted) || !errors.Is(err, row.ready) || evidence.Status != "not_started" || evidence.ExitCode != nil || !strings.Contains(evidence.Failure, row.ready.Error()) {
				t.Fatalf("a run whose work was done = %v, evidence %+v; want it never started, saying why", err, evidence)
			}
			if _, err := os.Stat(filepath.Join(executor.Admission.Dir, "slot-1")); !os.IsNotExist(err) {
				t.Fatalf("the run kept its turn: %v", err)
			}
		})
	}
}

func TestExecutorLimitStopsOnceTheCommandExits(t *testing.T) {
	if os.Getenv(limitsRole) == "starter" {
		// The command starts a child and exits at once, so its own run never
		// waits on a child's start, which a loaded machine stretched past a
		// four second limit on 2026-10-14.
		child := exec.Command("ping", "-n", "60", "127.0.0.1")
		if err := child.Start(); err != nil {
			os.Exit(8)
		}
		os.Exit(0)
	}
	// Arrange
	store := t.TempDir()
	t.Setenv("CFO_VERIFY_DIR", store)
	// The command exits well inside its limit, and its child lives through
	// the linger, past the limit, which would end it if the limit still ran.
	executor := Executor{Admission: admission(t), Monitor: Monitor{Poll: 10 * time.Millisecond, Grace: 10 * time.Millisecond, Linger: 12 * time.Second, Headroom: plentyOfMemory}}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Act
	_, record, err := executor.RunWithin(ctx, execx.Request{Dir: t.TempDir(), Name: os.Args[0], Args: []string{"-test.run=^TestExecutorLimitStopsOnceTheCommandExits$"}, Env: append(os.Environ(), limitsRole+"=starter")}, 8*time.Second)

	// Assert
	if err != nil {
		t.Fatalf("a command that exited inside its limit = %v, want its limit stopped at its exit", err)
	}
	if evidence := readExecution(t, record); evidence.Status != "passed" || len(evidence.Measurement.Leftovers) != 1 {
		t.Fatalf("evidence = %+v, want a pass with its child named as left running", evidence)
	}
}

func TestExecutorGivesTheCommandAnEmptyInputWithoutOne(t *testing.T) {
	if os.Getenv(limitsRole) == "reader" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			os.Exit(4)
		}
		if len(data) != 0 {
			os.Exit(3)
		}
		os.Exit(0)
	}
	// Arrange
	t.Setenv("CFO_VERIFY_DIR", t.TempDir())
	executor := Executor{Admission: admission(t), Monitor: Monitor{Poll: 10 * time.Millisecond, Headroom: plentyOfMemory}}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Act
	result, _, err := executor.RunWithin(ctx, execx.Request{Dir: t.TempDir(), Name: os.Args[0], Args: []string{"-test.run=^TestExecutorGivesTheCommandAnEmptyInputWithoutOne$"}, Env: append(os.Environ(), limitsRole+"=reader")}, 0)

	// Assert
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("a command reading its input = %+v, %v, want an empty input read to its end", result, err)
	}
}

func TestExecutorInterruptsACommandWhoseOutputPassesItsLimit(t *testing.T) {
	if os.Getenv(limitsRole) == "chatty" {
		os.Stdout.Write(make([]byte, 64<<10))
		time.Sleep(time.Minute)
		os.Exit(9)
	}
	// Arrange
	t.Setenv("CFO_VERIFY_DIR", t.TempDir())
	executor := Executor{OutputLimit: 1 << 10, Admission: admission(t), Monitor: Monitor{Poll: 10 * time.Millisecond, Grace: 10 * time.Millisecond, Headroom: plentyOfMemory}}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Act
	_, record, err := executor.RunWithin(ctx, execx.Request{Dir: t.TempDir(), Name: os.Args[0], Args: []string{"-test.run=^TestExecutorInterruptsACommandWhoseOutputPassesItsLimit$"}, Env: append(os.Environ(), limitsRole+"=chatty")}, 0)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "output") {
		t.Fatalf("a command past its output limit = %v", err)
	}
	if evidence := readExecution(t, record); evidence.Status != "failed" || evidence.Measurement.State != Interrupted || !evidence.Measurement.CleanupConfirmed {
		t.Fatalf("evidence = %+v", evidence)
	}
}

func TestExecutorAppliesTheStoresReserveAndNeverALowerOne(t *testing.T) {
	if os.Getenv(limitsRole) == "sleeper" {
		time.Sleep(time.Minute)
		os.Exit(9)
	}
	for _, test := range []struct {
		name    string
		setting string
		given   Headroom
		during  uint64
	}{
		{name: "the store's reserve over the default", setting: `{"physical_gib": 3, "commit_gib": 2}`, during: 5 << 29},
		{name: "a lower reserve the caller gives", given: Headroom{PhysicalAvailable: 1 << 30, CommitAvailable: 1 << 30}, during: 3 << 29},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			t.Setenv("CFO_VERIFY_DIR", t.TempDir())
			var isStarted atomic.Bool
			executor := Executor{Admission: admission(t), Monitor: Monitor{Reserve: test.given, Sustain: time.Millisecond, Poll: 10 * time.Millisecond, Grace: 10 * time.Millisecond,
				Headroom: func() (Headroom, error) {
					if isStarted.Load() {
						return Headroom{PhysicalAvailable: test.during, CommitAvailable: 8 << 30}, nil
					}
					return Headroom{PhysicalAvailable: 8 << 30, CommitAvailable: 8 << 30}, nil
				},
				Observe: func(Measurement) error { isStarted.Store(true); return nil },
			}}
			if test.setting != "" {
				if err := os.WriteFile(filepath.Join(executor.Admission.Dir, "reserve.json"), []byte(test.setting), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			// Act
			_, _, err := executor.RunWithin(ctx, execx.Request{Dir: t.TempDir(), Name: os.Args[0], Args: []string{"-test.run=^TestExecutorAppliesTheStoresReserveAndNeverALowerOne$"}, Env: append(os.Environ(), limitsRole+"=sleeper")}, 0)

			// Assert
			if err == nil || !strings.Contains(err.Error(), "reserve pressure") {
				t.Fatalf("free memory under the reserve the run must keep = %v, want reserve pressure", err)
			}
		})
	}
}

// A nested turn in the command, as cfo gate turn under cfo verify takes,
// runs inside the executor's turn, and what it lets go of when it ends is
// still the executor's to end and name.
func TestExecutorNamesWhatANestedTurnLeftRunning(t *testing.T) {
	switch os.Getenv(limitsRole) {
	case "nested":
		// Taking a turn of its own would wait for the executor's for good,
		// so a bounded wait proves the turn it gets is the executor's.
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		turn, err := Admission{Dir: os.Getenv("VERIFY_LIMITS_ADMISSION"), Available: func() (uint64, error) { return 16 << 30, nil }, Limit: time.Minute, Poll: 5 * time.Millisecond}.Wait(ctx)
		cancel()
		if err != nil || turn.Token() == "" {
			os.Exit(6)
		}
		child := execx.Command(os.Args[0], os.Args[1:]...)
		child.Env = append(os.Environ(), limitsRole+"=nested-child")
		tree, err := execx.StartTree(child)
		if err != nil {
			os.Exit(5)
		}
		for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if data, err := os.ReadFile("child.pid"); err == nil && len(data) > 0 {
				break
			}
		}
		if err := tree.Release(); err != nil {
			os.Exit(4)
		}
		if err := turn.Release(); err != nil {
			os.Exit(3)
		}
		os.Exit(0)
	case "nested-child":
		if err := os.WriteFile("child.pid", []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			os.Exit(7)
		}
		time.Sleep(time.Minute)
		os.Exit(9)
	}
	// Arrange
	t.Setenv("CFO_VERIFY_DIR", t.TempDir())
	root := t.TempDir()
	executor := Executor{Admission: admission(t), Monitor: Monitor{Poll: 10 * time.Millisecond, Linger: 300 * time.Millisecond, Headroom: plentyOfMemory}}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Act
	result, record, err := executor.RunWithin(ctx, execx.Request{Dir: root, Name: os.Args[0], Args: []string{"-test.run=^TestExecutorNamesWhatANestedTurnLeftRunning$"}, Env: append(os.Environ(), limitsRole+"=nested", "VERIFY_LIMITS_ADMISSION="+executor.Admission.Dir)}, 0)

	// Assert
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("a command with a nested turn = %+v, %v", result, err)
	}
	childPID, err := strconv.Atoi(strings.TrimSpace(string(waitForFile(t, filepath.Join(root, "child.pid"), time.Minute))))
	if err != nil {
		t.Fatal(err)
	}
	evidence := readExecution(t, record)
	if len(evidence.Measurement.Leftovers) != 1 || evidence.Measurement.Leftovers[0].PID != childPID {
		t.Fatalf("leftovers = %+v, want the nested tree's child %d", evidence.Measurement.Leftovers, childPID)
	}
	if child, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(childPID)); err == nil {
		isEnded := endsSoon(child)
		windows.CloseHandle(child)
		if !isEnded {
			t.Fatal("the nested tree's child still runs after the run ended")
		}
	}
}

// A machine service the command started, as cfo services up starts Docker
// Desktop, serves the whole machine: the run's end leaves it running and
// names it apart from what it ended, and the run keeps its pass and gives
// its turn back.
func TestExecutorSparesAMachineServiceTheCommandStarted(t *testing.T) {
	if os.Getenv(limitsRole) == "service-starter" {
		for _, name := range []string{"com.docker.standin", "worker"} {
			started := exec.Command(filepath.Join(os.Getenv("VERIFY_LIMITS_PROGRAMS"), name+".exe"))
			if started.Start() != nil {
				os.Exit(8)
			}
			// Written whole and then named, so the test never reads half a pid.
			if os.WriteFile(name+".pid.tmp", []byte(strconv.Itoa(started.Process.Pid)), 0o600) != nil || os.Rename(name+".pid.tmp", name+".pid") != nil {
				os.Exit(7)
			}
		}
		os.Exit(0)
	}
	// Arrange
	t.Setenv("CFO_VERIFY_DIR", t.TempDir())
	root := t.TempDir()
	programs := t.TempDir()
	standin.RemoveAtCleanup(t, programs)
	for _, name := range []string{"com.docker.standin", "worker"} {
		standin.Put(t, filepath.Join(programs, name+".exe"))
	}
	executor := Executor{Admission: admission(t), Monitor: Monitor{Poll: 10 * time.Millisecond, Linger: 300 * time.Millisecond, Headroom: plentyOfMemory}}
	environment := append(os.Environ(), limitsRole+"=service-starter", "VERIFY_LIMITS_PROGRAMS="+programs)
	environment = append(environment, standin.Env("", standin.Rule{Any: true, Hold: true})...)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Act
	result, record, err := executor.RunWithin(ctx, execx.Request{Dir: root, Name: os.Args[0], Args: []string{"-test.run=^TestExecutorSparesAMachineServiceTheCommandStarted$"}, Env: environment}, 0)

	// Assert
	pids := map[string]int{}
	for _, name := range []string{"com.docker.standin", "worker"} {
		pid, parseError := strconv.Atoi(string(waitForFile(t, filepath.Join(root, name+".pid"), time.Minute)))
		if parseError != nil {
			t.Fatal(parseError)
		}
		pids[name] = pid
	}
	service := standin.Hold(t, pids["com.docker.standin"])
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("a command that started a machine service = %+v, %v", result, err)
	}
	evidence := readExecution(t, record)
	if evidence.Status != "passed" || !evidence.Measurement.CleanupConfirmed {
		t.Fatalf("evidence = %+v, want a pass with its cleanup confirmed", evidence)
	}
	if spared := evidence.Measurement.Spared; len(spared) != 1 || spared[0].PID != pids["com.docker.standin"] || !strings.EqualFold(spared[0].Name, "com.docker.standin.exe") {
		t.Fatalf("spared = %+v, want the machine service %d", spared, pids["com.docker.standin"])
	}
	if leftovers := evidence.Measurement.Leftovers; len(leftovers) != 1 || leftovers[0].PID != pids["worker"] {
		t.Fatalf("leftovers = %+v, want the worker %d alone", leftovers, pids["worker"])
	}
	if event, err := windows.WaitForSingleObject(service, 0); err != nil || event != uint32(windows.WAIT_TIMEOUT) {
		t.Fatal("the machine service was ended with the run")
	}
	if worker, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pids["worker"])); err == nil {
		isEnded := endsSoon(worker)
		windows.CloseHandle(worker)
		if !isEnded {
			t.Fatal("the worker still runs after the run ended")
		}
	}
	holders, waiters, err := Line(executor.Admission.Dir)
	if err != nil || len(holders)+len(waiters) != 0 {
		t.Fatalf("custody leaked: %v, %v, %v", holders, waiters, err)
	}
}

// A run whose end spared a machine service but whose evidence could not be
// written keeps the service in what it retries, so the retry completes the
// run's custody beside the service rather than waiting for a job that never
// empties.
func TestExecutorRetryKeepsTheServiceItsRunSpared(t *testing.T) {
	// Arrange
	store := t.TempDir()
	t.Setenv("CFO_VERIFY_DIR", store)
	root := t.TempDir()
	programs := t.TempDir()
	standin.RemoveAtCleanup(t, programs)
	for _, name := range []string{"com.docker.standin", "worker"} {
		standin.Put(t, filepath.Join(programs, name+".exe"))
	}
	var record string
	executor := Executor{Admission: admission(t), Monitor: Monitor{Poll: 10 * time.Millisecond, Linger: 300 * time.Millisecond, Headroom: plentyOfMemory,
		Observe: func(measurement Measurement) error {
			if measurement.State == Running || record != "" {
				return nil
			}
			// The record's place is taken once the end is measured, so the
			// run cannot write what became of it.
			records, err := filepath.Glob(filepath.Join(store, "executions", "*.json"))
			if err != nil || len(records) != 1 {
				return fmt.Errorf("records = %v, %v", records, err)
			}
			record = records[0]
			if err := os.Rename(record, record+".saved"); err != nil {
				return err
			}
			if err := os.Mkdir(record, 0o700); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(record, "occupied"), nil, 0o600)
		}}}
	environment := append(os.Environ(), limitsRole+"=service-starter", "VERIFY_LIMITS_PROGRAMS="+programs)
	environment = append(environment, standin.Env("", standin.Rule{Any: true, Hold: true})...)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	_, _, runError := executor.RunWithin(ctx, execx.Request{Dir: root, Name: os.Args[0], Args: []string{"-test.run=^TestExecutorSparesAMachineServiceTheCommandStarted$"}, Env: environment}, 0)
	service, err := strconv.Atoi(string(waitForFile(t, filepath.Join(root, "com.docker.standin.pid"), time.Minute)))
	if err != nil {
		t.Fatal(err)
	}
	standin.Hold(t, service)
	if runError == nil || record == "" {
		t.Fatalf("run = %v with record %q, want its evidence write to fail", runError, record)
	}
	for _, step := range []error{os.Remove(filepath.Join(record, "occupied")), os.Remove(record), os.Rename(record+".saved", record)} {
		if step != nil {
			t.Fatal(step)
		}
	}

	// Act
	err = executor.RetryCleanup(context.Background())

	// Assert
	if err != nil {
		t.Fatalf("retry = %v, want the run's custody completed beside the service", err)
	}
	evidence := readExecution(t, record)
	if !evidence.Measurement.CleanupConfirmed || len(evidence.Measurement.Spared) != 1 || evidence.Measurement.Spared[0].PID != service {
		t.Fatalf("evidence = %+v, want the cleanup confirmed and the service %d named", evidence.Measurement, service)
	}
	holders, waiters, err := Line(executor.Admission.Dir)
	if err != nil || len(holders)+len(waiters) != 0 {
		t.Fatalf("custody leaked: %v, %v, %v", holders, waiters, err)
	}
}

// Given its caller's own output, as cfo gate turn gives its console or pipe,
// the command writes there, the execution keeps no logs, and the result
// holds no output: the caller keeps it.
func TestExecutorWritesToItsCallersOutputWhenGivenIt(t *testing.T) {
	// Arrange
	store := t.TempDir()
	t.Setenv("CFO_VERIFY_DIR", store)
	root := t.TempDir()
	standin.RemoveAtCleanup(t, root)
	program := filepath.Join(root, "workload.exe")
	standin.Put(t, program)
	output, err := os.Create(filepath.Join(t.TempDir(), "stdout"))
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	errorsOutput, err := os.Create(filepath.Join(t.TempDir(), "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	defer errorsOutput.Close()
	executor := Executor{Admission: admission(t), Monitor: Monitor{Poll: 10 * time.Millisecond, Headroom: plentyOfMemory}, Stdout: output, Stderr: errorsOutput}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Act
	result, record, err := executor.RunWithin(ctx, execx.Request{Dir: root, Name: program, Env: append(os.Environ(), standin.Env("", standin.Rule{Any: true, Stdout: "proof", Stderr: "said", Exit: 0})...)}, 0)

	// Assert
	if err != nil || result.ExitCode != 0 || len(result.Stdout) != 0 || len(result.Stderr) != 0 {
		t.Fatalf("result = %+v, %v; want a pass with no output of its own", result, err)
	}
	for file, want := range map[*os.File]string{output: "proof", errorsOutput: "said"} {
		if written, err := os.ReadFile(file.Name()); err != nil || string(written) != want {
			t.Fatalf("%s holds %q, %v; want %q", file.Name(), written, err, want)
		}
	}
	evidence := readExecution(t, record)
	if evidence.Status != "passed" || evidence.StdoutLog != "" || evidence.StderrLog != "" {
		t.Fatalf("evidence = %+v; want a pass that names no logs", evidence)
	}
	if logs, err := filepath.Glob(filepath.Join(store, "executions", "*.log")); err != nil || len(logs) != 0 {
		t.Fatalf("logs = %v, %v; want none", logs, err)
	}
}

// A command that shares its caller's console answers an interrupt itself, as
// a Ctrl+C at the console reaches it directly: the run's context ending
// leaves it running until it ends, with its own exit code. A command in a
// group of its own is sent a Ctrl+Break and ended with its run.
func TestExecutorLetsACommandSharingItsConsoleAnswerAnInterrupt(t *testing.T) {
	if os.Getenv(limitsRole) == "answers-late" {
		if os.WriteFile("started", []byte("started"), 0o600) != nil {
			os.Exit(8)
		}
		time.Sleep(time.Second)
		os.Exit(3)
	}
	for _, test := range []struct {
		name            string
		isConsoleShared bool
		wantOwnExitCode bool
	}{
		{name: "sharing the console", isConsoleShared: true, wantOwnExitCode: true},
		{name: "in a group of its own", isConsoleShared: false, wantOwnExitCode: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			t.Setenv("CFO_VERIFY_DIR", t.TempDir())
			root := t.TempDir()
			executor := Executor{Admission: admission(t), Monitor: Monitor{Poll: 10 * time.Millisecond, Grace: time.Millisecond, Headroom: plentyOfMemory}, IsConsoleShared: test.isConsoleShared}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go func() {
				waitForFile(t, filepath.Join(root, "started"), time.Minute)
				cancel()
			}()

			// Act
			result, _, err := executor.RunWithin(ctx, execx.Request{Dir: root, Name: os.Args[0], Args: []string{"-test.run=^TestExecutorLetsACommandSharingItsConsoleAnswerAnInterrupt$"}, Env: append(os.Environ(), limitsRole+"=answers-late")}, 0)

			// Assert
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("run = %+v, %v; want it recorded as interrupted", result, err)
			}
			if isOwn := result.ExitCode == 3; isOwn != test.wantOwnExitCode {
				t.Fatalf("exit code = %d; want the command's own 3: %v", result.ExitCode, test.wantOwnExitCode)
			}
		})
	}
}

// A run that never started its command says why in the error's kind: no
// turn when it took none, so a caller such as cfo gate turn exits 125, and
// not started when it took its turn and the command could not start, 126.
func TestExecutorSaysWhyACommandNeverStarted(t *testing.T) {
	for _, test := range []struct {
		name    string
		program string
		ctx     func() context.Context
		want    error
	}{
		{name: "no turn", program: os.Args[0], ctx: func() context.Context {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx
		}, want: ErrNoTurn},
		{name: "not started", program: filepath.Join(t.TempDir(), "missing.exe"), ctx: context.Background, want: ErrNotStarted},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			t.Setenv("CFO_VERIFY_DIR", t.TempDir())
			executor := Executor{Admission: admission(t), Monitor: Monitor{Poll: 10 * time.Millisecond, Headroom: plentyOfMemory}}

			// Act
			result, _, err := executor.RunWithin(test.ctx(), execx.Request{Dir: t.TempDir(), Name: test.program}, 0)

			// Assert
			if !errors.Is(err, test.want) || result.ExitCode != -1 {
				t.Fatalf("run = %+v, %v; want %v and no exit code", result, err, test.want)
			}
			other := ErrNotStarted
			if test.want == ErrNotStarted {
				other = ErrNoTurn
			}
			if errors.Is(err, other) {
				t.Fatalf("run error %v is %v as well", err, other)
			}
		})
	}
}

// An interrupt that comes after the run took its turn and before a command
// that answers interrupts itself started never reaches the command, so the
// run ends there, with no turn's result, and starts nothing.
func TestExecutorEndsASharedRunInterruptedBeforeItsCommandStarted(t *testing.T) {
	if os.Getenv(limitsRole) == "must-not-start" {
		_ = os.WriteFile("started", []byte("started"), 0o600)
		os.Exit(0)
	}
	// Arrange
	t.Setenv("CFO_VERIFY_DIR", t.TempDir())
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	executor := Executor{Admission: admission(t), Monitor: Monitor{Poll: 10 * time.Millisecond, Headroom: plentyOfMemory}, IsConsoleShared: true, Took: func(time.Duration) { cancel() }}

	// Act
	result, _, err := executor.RunWithin(ctx, execx.Request{Dir: root, Name: os.Args[0], Args: []string{"-test.run=^TestExecutorEndsASharedRunInterruptedBeforeItsCommandStarted$"}, Env: append(os.Environ(), limitsRole+"=must-not-start")}, 0)

	// Assert
	if !errors.Is(err, ErrNoTurn) || !errors.Is(err, context.Canceled) || result.ExitCode != -1 {
		t.Fatalf("run = %+v, %v; want no turn's result for an interrupt", result, err)
	}
	if _, statError := os.Stat(filepath.Join(root, "started")); statError == nil {
		t.Fatal("the command started after its run was interrupted")
	}
	holders, waiters, lineError := Line(executor.Admission.Dir)
	if lineError != nil || len(holders)+len(waiters) != 0 {
		t.Fatalf("custody leaked: %v, %v, %v", holders, waiters, lineError)
	}
}

// An interrupt that comes while the source is read, before the command
// starts, ends a shared run as one before the read does: the read comes
// before the check for an interrupt, so the command never starts.
func TestExecutorEndsASharedRunInterruptedWhileItsSourceIsRead(t *testing.T) {
	if os.Getenv(limitsRole) == "must-not-start" {
		_ = os.WriteFile("started", []byte("started"), 0o600)
		os.Exit(0)
	}
	// Arrange
	t.Setenv("CFO_VERIFY_DIR", t.TempDir())
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	executor := Executor{Admission: admission(t), Monitor: Monitor{Poll: 10 * time.Millisecond, Headroom: plentyOfMemory}, IsConsoleShared: true}
	executor.Source = func(context.Context, string) (Source, error) {
		cancel()
		return Source{Commit: "c1", Tree: "t1", IsComplete: true}, nil
	}

	// Act
	result, _, err := executor.RunWithin(ctx, execx.Request{Dir: root, Name: os.Args[0], Args: []string{"-test.run=^TestExecutorEndsASharedRunInterruptedWhileItsSourceIsRead$"}, Env: append(os.Environ(), limitsRole+"=must-not-start")}, 0)

	// Assert
	if !errors.Is(err, ErrNoTurn) || !errors.Is(err, context.Canceled) || result.ExitCode != -1 {
		t.Fatalf("run = %+v, %v; want no turn's result for an interrupt", result, err)
	}
	if _, statError := os.Stat(filepath.Join(root, "started")); statError == nil {
		t.Fatal("the command started after its run was interrupted")
	}
	holders, waiters, lineError := Line(executor.Admission.Dir)
	if lineError != nil || len(holders)+len(waiters) != 0 {
		t.Fatalf("custody leaked: %v, %v, %v", holders, waiters, lineError)
	}
}

// A running sample is progress, not the run's evidence, so one that cannot
// be written ends no command: the command runs to its own end, and the run
// keeps its exit code.
func TestExecutorKeepsRunningWhenASampleCannotBeWritten(t *testing.T) {
	if os.Getenv(limitsRole) == "runs-a-while" {
		time.Sleep(12 * time.Second)
		os.Exit(4)
	}
	// Arrange
	store := t.TempDir()
	t.Setenv("CFO_VERIFY_DIR", store)
	root := t.TempDir()
	var record string
	executor := Executor{Admission: admission(t), Monitor: Monitor{Poll: 50 * time.Millisecond, Headroom: plentyOfMemory,
		Observe: func(measurement Measurement) error {
			if measurement.State != Running || record != "" {
				return nil
			}
			// The record's place is taken after the first sample, so the
			// next, ten seconds on, cannot be written.
			records, err := filepath.Glob(filepath.Join(store, "executions", "*.json"))
			if err != nil || len(records) != 1 {
				return fmt.Errorf("records = %v, %v", records, err)
			}
			record = records[0]
			if err := os.Rename(record, record+".saved"); err != nil {
				return err
			}
			if err := os.Mkdir(record, 0o700); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(record, "occupied"), nil, 0o600); err != nil {
				return err
			}
			// Its place is given back before the command ends, so the run's
			// final measurement can be written.
			go func() {
				time.Sleep(11 * time.Second)
				_ = os.Remove(filepath.Join(record, "occupied"))
				_ = os.Remove(record)
				_ = os.Rename(record+".saved", record)
			}()
			return nil
		}}}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Act
	result, _, err := executor.RunWithin(ctx, execx.Request{Dir: root, Name: os.Args[0], Args: []string{"-test.run=^TestExecutorKeepsRunningWhenASampleCannotBeWritten$"}, Env: append(os.Environ(), limitsRole+"=runs-a-while")}, 0)

	// Assert
	if err != nil || result.ExitCode != 4 {
		t.Fatalf("run = %+v, %v; want the command's own 4 after a sample could not be written", result, err)
	}
	if record == "" {
		t.Fatal("no sample's record was taken away, so the test proved nothing")
	}
}

// A shared run's command answers an interrupt itself and runs to its own
// end, so the source is read once it has ended whatever the interrupt: the
// read is not made on the interrupted context.
func TestExecutorReadsTheEndOfASharedRunPastAnInterrupt(t *testing.T) {
	if os.Getenv(limitsRole) == "ends-a-moment-later" {
		time.Sleep(time.Second)
		os.Exit(0)
	}
	// Arrange
	t.Setenv("CFO_VERIFY_DIR", t.TempDir())
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var endRead error
	reads := 0
	executor := Executor{Admission: admission(t), Monitor: Monitor{Poll: 10 * time.Millisecond, Headroom: plentyOfMemory}, IsConsoleShared: true}
	executor.Source = func(read context.Context, dir string) (Source, error) {
		reads++
		if reads == 1 {
			time.AfterFunc(200*time.Millisecond, cancel) // stall-accepted: the test means this timer to fire while the command runs, and a command that ends first still passes.
		} else {
			endRead = read.Err()
		}
		return Source{Commit: "c1", Tree: "t1", IsComplete: true}, nil
	}

	// Act
	_, path, _ := executor.RunWithin(ctx, execx.Request{Dir: root, Name: os.Args[0], Args: []string{"-test.run=^TestExecutorReadsTheEndOfASharedRunPastAnInterrupt$"}, Env: append(os.Environ(), limitsRole+"=ends-a-moment-later")}, 0)

	// Assert
	record, err := LoadExecution(path)
	if err != nil {
		t.Fatal(err)
	}
	if reads != 2 || endRead != nil || record.Source.End == nil {
		t.Errorf("reads = %d, the end read's context said %v, record reads = %+v; want the end read made on a context no interrupt ended", reads, endRead, record.Source)
	}
}
