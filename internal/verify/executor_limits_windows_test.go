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
		for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
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
	childPID, err := strconv.Atoi(strings.TrimSpace(string(waitForFile(t, filepath.Join(root, "child.pid"), time.Second))))
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
	go func() {
		time.Sleep(5 * time.Second)
		release.Close()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Act
	result, record, err := executor.RunWithin(ctx, execx.Request{Dir: root, Name: program, Env: append(os.Environ(), standin.Env("", standin.Rule{Any: true, Stdout: "ran", Exit: 0})...)}, 3*time.Second)

	// Assert
	if err != nil || result.ExitCode != 0 || string(result.Stdout) != "ran" {
		t.Fatalf("a quick command behind a longer wait = %+v, %v", result, err)
	}
	if evidence := readExecution(t, record); evidence.Status != "passed" || evidence.QueueSeconds < 4 {
		t.Fatalf("evidence = %+v, want a pass after a wait longer than the limit", evidence)
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
	if !errors.Is(err, ErrLimit) || time.Since(started) > 15*time.Second {
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

func TestExecutorLimitStopsOnceTheCommandExits(t *testing.T) {
	// Arrange
	store := t.TempDir()
	t.Setenv("CFO_VERIFY_DIR", store)
	// The command exits well inside its limit and leaves a child past it.
	executor := Executor{Admission: admission(t), Monitor: Monitor{Poll: 10 * time.Millisecond, Grace: 10 * time.Millisecond, Linger: 6 * time.Second, Headroom: plentyOfMemory}}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Act
	_, record, err := executor.RunWithin(ctx, execx.Request{Dir: t.TempDir(), Name: os.Args[0], Args: []string{"-test.run=^TestExecutorEndsAndNamesWhatTheCommandLeftRunning$"}, Env: append(os.Environ(), limitsRole+"=parent")}, 4*time.Second)

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
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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
		for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
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
	childPID, err := strconv.Atoi(strings.TrimSpace(string(waitForFile(t, filepath.Join(root, "child.pid"), time.Second))))
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
		pid, parseError := strconv.Atoi(string(waitForFile(t, filepath.Join(root, name+".pid"), time.Second)))
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
	service, err := strconv.Atoi(string(waitForFile(t, filepath.Join(root, "com.docker.standin.pid"), time.Second)))
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
