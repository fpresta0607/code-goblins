package verify

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/standin"
)

func TestExecutorRecordsActualExitOutputAndWholeJobCleanup(t *testing.T) {
	for _, code := range []int{0, 7} {
		t.Run(string(rune('0'+code)), func(t *testing.T) {
			// Arrange
			store := t.TempDir()
			t.Setenv("CFO_VERIFY_DIR", store)
			root := t.TempDir()
			standin.RemoveAtCleanup(t, root)
			program := filepath.Join(root, "workload.exe")
			standin.Put(t, program)
			executor := Executor{Admission: admission(t), Monitor: Monitor{Poll: time.Millisecond, Headroom: func() (Headroom, error) {
				return Headroom{PhysicalAvailable: 8 << 30, CommitAvailable: 8 << 30}, nil
			}}}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			// Act
			result, err := executor.Run(ctx, execx.Request{Dir: root, Name: program, Env: append(os.Environ(), standin.Env("", standin.Rule{Any: true, Stdout: "proof", Exit: code})...)})

			// Assert
			if err != nil || result.ExitCode != code || string(result.Stdout) != "proof" {
				t.Fatalf("result = %+v, %v", result, err)
			}
			files, err := filepath.Glob(filepath.Join(store, "executions", "*.json"))
			if err != nil || len(files) != 1 {
				t.Fatalf("records = %v, %v", files, err)
			}
			data, err := fsx.ReadFile(files[0])
			if err != nil {
				t.Fatal(err)
			}
			var record ExecutionRecord
			if err := json.Unmarshal(data, &record); err != nil {
				t.Fatal(err)
			}
			if record.ExitCode == nil || *record.ExitCode != code || !record.Measurement.CleanupConfirmed || record.Measurement.Usage.TotalProcesses != 1 {
				t.Fatalf("evidence = %+v", record)
			}
			if (record.Status == "passed") != (code == 0) {
				t.Fatalf("status = %s, code = %d", record.Status, code)
			}
			logged, err := fsx.ReadFile(record.StdoutLog)
			if err != nil || string(logged) != "proof" {
				t.Fatalf("durable stdout = %q, %v", logged, err)
			}
			holders, waiters, err := Line(executor.Admission.Dir)
			if err != nil || len(holders)+len(waiters) != 0 {
				t.Fatalf("custody leaked: %v, %v, %v", holders, waiters, err)
			}
		})
	}
}

func TestExecutorTracksDescendantsAfterTheParentExits(t *testing.T) {
	const roleVariable = "VERIFY_EXECUTOR_FIXTURE"
	if role := os.Getenv(roleVariable); role != "" {
		if role == "child" {
			// Written whole and then named, so the run's Observe, which
			// fails the run on a pid it cannot read, never reads an empty
			// file between its creation and its write.
			if os.WriteFile("child.pid.tmp", []byte(strconv.Itoa(os.Getpid())), 0600) != nil || os.Rename("child.pid.tmp", "child.pid") != nil {
				os.Exit(7)
			}
			time.Sleep(20 * time.Second)
			os.Exit(9)
		}
		child := execx.Command(os.Args[0], "-test.run=^TestExecutorTracksDescendantsAfterTheParentExits$")
		child.Env = append(os.Environ(), roleVariable+"=child")
		if err := child.Start(); err != nil {
			os.Exit(8)
		}
		os.Exit(0)
	}
	// Arrange
	store := t.TempDir()
	t.Setenv("CFO_VERIFY_DIR", store)
	root := t.TempDir()
	isParentGone := false
	var childHandle windows.Handle
	t.Cleanup(func() {
		if childHandle != 0 {
			windows.CloseHandle(childHandle)
		}
	})
	// The child outlives the linger, so only pressure can end the run.
	executor := Executor{Admission: admission(t), Monitor: Monitor{Poll: time.Millisecond, Grace: time.Millisecond, Sustain: time.Millisecond, Linger: time.Minute,
		Headroom: func() (Headroom, error) {
			if isParentGone {
				return Headroom{PhysicalAvailable: 1 << 30, CommitAvailable: 8 << 30}, nil
			}
			return Headroom{PhysicalAvailable: 8 << 30, CommitAvailable: 8 << 30}, nil
		}, Observe: func(measurement Measurement) error {
			data, err := os.ReadFile(filepath.Join(root, "child.pid"))
			if err == nil && measurement.Usage.TotalProcesses == 2 && measurement.Usage.ActiveProcesses == 1 {
				childPID, err := strconv.Atoi(string(data))
				if err != nil {
					return err
				}
				if childHandle == 0 {
					childHandle, err = windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(childPID))
					if err != nil {
						return err
					}
				}
				isParentGone = true
			}
			return nil
		}}}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Act
	result, err := executor.Run(ctx, execx.Request{Dir: root, Name: os.Args[0], Args: []string{"-test.run=^TestExecutorTracksDescendantsAfterTheParentExits$"}, Env: append(os.Environ(), roleVariable+"=parent")})

	// Assert
	if err == nil || !strings.Contains(err.Error(), "reserve pressure") || !isParentGone || result.ExitCode != 0 {
		t.Fatalf("parent result = %+v, %v, parent gone = %v, want reserve pressure", result, err, isParentGone)
	}
	data, readError := os.ReadFile(filepath.Join(root, "child.pid"))
	if readError != nil {
		t.Fatal(readError)
	}
	childPID, parseError := strconv.Atoi(string(data))
	if parseError != nil {
		t.Fatal(parseError)
	}
	isExited, waitError := windows.WaitForSingleObject(childHandle, 0)
	if waitError != nil || isExited != windows.WAIT_OBJECT_0 {
		t.Fatalf("owned child %d survived verified cleanup: wait=%d error=%v", childPID, isExited, waitError)
	}
	files, _ := filepath.Glob(filepath.Join(store, "executions", "*.json"))
	if len(files) != 1 {
		t.Fatalf("records = %v", files)
	}
	data, readError = os.ReadFile(files[0])
	if readError != nil {
		t.Fatal(readError)
	}
	var record ExecutionRecord
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	if record.Status != "failed" || record.Measurement.State != Interrupted || !record.Measurement.CleanupConfirmed || record.Measurement.Usage.ActiveProcesses != 0 || record.Measurement.Usage.TotalProcesses != 2 {
		t.Fatalf("pressure evidence = %+v", record)
	}
	if _, err := os.Stat(filepath.Join(executor.Admission.Dir, "slot-1")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("turn remains after confirmed cleanup: %v", err)
	}
}

func TestExecutorRefusesResourceWaitWithoutStartingCommand(t *testing.T) {
	// Arrange
	store := t.TempDir()
	t.Setenv("CFO_VERIFY_DIR", store)
	root := t.TempDir()
	executor := Executor{Admission: admission(t), Monitor: Monitor{Headroom: func() (Headroom, error) {
		return Headroom{PhysicalAvailable: 3 << 30, CommitAvailable: 8 << 30}, nil
	}}}
	executor.Admission.Limit = 10 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	// Act
	_, err := executor.Run(ctx, execx.Request{Dir: root, Name: filepath.Join(root, "must-not-start.exe")})

	// Assert
	if err == nil {
		t.Fatal("started below resource reserve")
	}
	files, _ := filepath.Glob(filepath.Join(store, "executions", "*.json"))
	if len(files) != 1 {
		t.Fatalf("records = %v", files)
	}
	data, err := fsx.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	var record ExecutionRecord
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	if record.Status != "not_started" || record.ExitCode != nil || len(record.Transitions) != 2 || record.QueueSeconds <= 0 {
		t.Fatalf("no-turn evidence = %+v", record)
	}
}

func TestExecutorRetainsCancellationFromTheFinalObserver(t *testing.T) {
	// Arrange
	store := t.TempDir()
	t.Setenv("CFO_VERIFY_DIR", store)
	root := t.TempDir()
	standin.RemoveAtCleanup(t, root)
	program := filepath.Join(root, "workload.exe")
	standin.Put(t, program)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	executor := Executor{Admission: admission(t), Monitor: Monitor{Poll: time.Millisecond,
		Headroom: func() (Headroom, error) { return Headroom{PhysicalAvailable: 8 << 30, CommitAvailable: 8 << 30}, nil },
		Observe: func(measurement Measurement) error {
			if measurement.State == Completed {
				cancel()
			}
			return nil
		}}}

	// Act
	result, err := executor.Run(ctx, execx.Request{Dir: root, Name: program})

	// Assert
	if !errors.Is(err, context.Canceled) || result.ExitCode != 0 {
		t.Fatalf("late cancellation = %+v, %v", result, err)
	}
	files, _ := filepath.Glob(filepath.Join(store, "executions", "*.json"))
	if len(files) != 1 {
		t.Fatalf("records = %v", files)
	}
	data, err := fsx.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	var record ExecutionRecord
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	if record.Status != "failed" || record.Measurement.State != Interrupted || !record.Measurement.CleanupConfirmed {
		t.Fatalf("late cancellation record = %+v", record)
	}
}

func TestExecutorFileInputDoesNotWaitForEOFWhenTheCommandEnds(t *testing.T) {
	// Arrange
	t.Setenv("CFO_VERIFY_DIR", t.TempDir())
	root := t.TempDir()
	standin.RemoveAtCleanup(t, root)
	program := filepath.Join(root, "workload.exe")
	standin.Put(t, program)
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	executor := Executor{Stdin: reader, Admission: admission(t), Monitor: Monitor{Poll: time.Millisecond,
		Headroom: func() (Headroom, error) { return Headroom{PhysicalAvailable: 8 << 30, CommitAvailable: 8 << 30}, nil },
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Act
	result, err := executor.Run(ctx, execx.Request{Dir: root, Name: program})

	// Assert
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("open stdin kept an exited command alive: %+v, %v", result, err)
	}
}

func TestExecutorAdmitsIndependentPhysicalAndCommitReserves(t *testing.T) {
	// Arrange
	t.Setenv("CFO_VERIFY_DIR", t.TempDir())
	root := t.TempDir()
	standin.RemoveAtCleanup(t, root)
	program := filepath.Join(root, "workload.exe")
	standin.Put(t, program)
	executor := Executor{Admission: admission(t), Monitor: Monitor{Poll: time.Millisecond,
		Reserve:  Headroom{PhysicalAvailable: 4 << 30, CommitAvailable: 12 << 30},
		Headroom: func() (Headroom, error) { return Headroom{PhysicalAvailable: 8 << 30, CommitAvailable: 16 << 30}, nil },
	}}
	executor.Admission.Limit = 10 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Act
	result, err := executor.Run(ctx, execx.Request{Dir: root, Name: program})

	// Assert
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("independent reserves incorrectly refused: %+v, %v", result, err)
	}
}

func TestExecutorRetainsCleanupForRetryWithoutChangingFailureToPass(t *testing.T) {
	// Arrange
	t.Setenv("CFO_VERIFY_DIR", t.TempDir())
	execution, err := BeginExecution(t.TempDir(), []string{"controlled fixture"})
	if err != nil {
		t.Fatal(err)
	}
	requested := admission(t)
	turn, err := requested.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer turn.Release()
	guard, err := turn.Protect(execution.ID(), execution.Path())
	if err != nil {
		t.Fatal(err)
	}
	for _, err := range []error{execution.Admit(0), execution.Start(), execution.Observe(Measurement{State: CleanupUncertain}), execution.Finish(-1, errors.New("initial cleanup failed"))} {
		if err != nil {
			t.Fatal(err)
		}
	}
	tree := &measuredTree{cleanupError: errors.New("still unavailable")}
	executor := Executor{pending: map[string]*pendingCleanup{execution.ID(): {tree: tree, turn: turn, guard: guard, execution: execution, isFinished: true}}}

	// Act
	firstError := executor.RetryCleanup(context.Background())
	turn.Release()
	if _, err := os.Stat(filepath.Join(requested.Dir, "slot-1")); err != nil || firstError == nil {
		t.Fatalf("uncertain retry released custody: %v, %v", firstError, err)
	}
	tree.cleanupError = nil
	if err := os.Rename(execution.Path(), execution.Path()+".saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(execution.Path(), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(execution.Path(), "occupied"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := executor.RetryCleanup(context.Background()); err == nil {
		t.Fatal("failed recovery evidence write was ignored")
	}
	if err := os.Remove(filepath.Join(execution.Path(), "occupied")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(execution.Path()); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(execution.Path()+".saved", execution.Path()); err != nil {
		t.Fatal(err)
	}
	tree.cleanupError = errors.New("job handle was already closed")
	finalError := executor.RetryCleanup(context.Background())

	// Assert
	if finalError != nil || len(executor.pending) != 0 || execution.record.Status != "failed" || !execution.record.Measurement.CleanupConfirmed {
		t.Fatalf("recovered cleanup = %+v, pending = %d, err = %v", execution.record, len(executor.pending), finalError)
	}
	if _, err := os.Stat(filepath.Join(requested.Dir, "slot-1")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("confirmed retry did not release custody: %v", err)
	}
}

func TestExecutorRetainsFinalWriteFailureAfterSuccessfulCleanup(t *testing.T) {
	// Arrange
	store := t.TempDir()
	t.Setenv("CFO_VERIFY_DIR", store)
	root := t.TempDir()
	standin.RemoveAtCleanup(t, root)
	program := filepath.Join(root, "workload.exe")
	standin.Put(t, program)
	var recordPath string
	executor := Executor{Admission: admission(t), Monitor: Monitor{Poll: time.Millisecond,
		Headroom: func() (Headroom, error) { return Headroom{PhysicalAvailable: 8 << 30, CommitAvailable: 8 << 30}, nil },
		Observe: func(measurement Measurement) error {
			if measurement.State != Completed {
				return nil
			}
			files, err := filepath.Glob(filepath.Join(store, "executions", "*.json"))
			if err != nil || len(files) != 1 {
				return errors.New("fixture execution record missing")
			}
			recordPath = files[0]
			if err := os.Rename(recordPath, recordPath+".saved"); err != nil {
				return err
			}
			if err := os.Mkdir(recordPath, 0700); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(recordPath, "occupied"), nil, 0600)
		}}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Act
	result, runError := executor.Run(ctx, execx.Request{Dir: root, Name: program})
	if recordPath == "" || runError == nil || len(executor.pending) != 1 {
		t.Fatalf("final-write failure was not retained: %+v, %v, pending=%d", result, runError, len(executor.pending))
	}
	if err := os.Remove(filepath.Join(recordPath, "occupied")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(recordPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(recordPath+".saved", recordPath); err != nil {
		t.Fatal(err)
	}
	retryError := executor.RetryCleanup(ctx)

	// Assert
	if retryError != nil || len(executor.pending) != 0 {
		t.Fatalf("final write retry = %v, pending=%d", retryError, len(executor.pending))
	}
	data, err := fsx.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	var record ExecutionRecord
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	if record.Status != "failed" || record.ExitCode == nil || *record.ExitCode != 0 || record.Measurement.State != Interrupted || !record.Measurement.CleanupConfirmed {
		t.Fatalf("retry lost failure evidence: %+v", record)
	}
}

type delayedCleanup struct {
	entered  chan struct{}
	released chan struct{}
}

type canceledAdmission struct {
	context.Context
	cancel     context.CancelFunc
	dir        string
	t          *testing.T
	isCanceled bool
}

func (ctx *canceledAdmission) Err() error {
	if !ctx.isCanceled {
		if _, err := os.Stat(filepath.Join(ctx.dir, "slot-1")); err == nil {
			ctx.isCanceled = true
			path := filepath.Join(ctx.dir, "slot-1.run")
			if err := os.Remove(path); err != nil {
				ctx.t.Fatal(err)
			}
			if err := os.Mkdir(path, 0700); err != nil {
				ctx.t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(path, "occupied"), nil, 0600); err != nil {
				ctx.t.Fatal(err)
			}
			ctx.cancel()
		}
	}
	return ctx.Context.Err()
}

func TestExecutorRetainsFailedReleaseWhenAdmissionIsCanceled(t *testing.T) {
	// Arrange
	t.Setenv("CFO_VERIFY_DIR", t.TempDir())
	requested := admission(t)
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &canceledAdmission{Context: parent, cancel: cancel, dir: requested.Dir, t: t}
	executor := Executor{Admission: requested, Monitor: Monitor{Headroom: func() (Headroom, error) {
		return Headroom{PhysicalAvailable: 8 << 30, CommitAvailable: 8 << 30}, nil
	}}}

	// Act
	_, err := executor.Run(ctx, execx.Request{Dir: t.TempDir(), Name: "must-not-start.exe"})

	// Assert
	if !errors.Is(err, context.Canceled) || len(executor.pending) != 1 {
		t.Fatalf("canceled admission lost release owner: %v, pending=%d", err, len(executor.pending))
	}
	path := filepath.Join(requested.Dir, "slot-1.run")
	if err := os.Remove(filepath.Join(path, "occupied")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := executor.RetryCleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(requested.Dir, "slot-1")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("recovery left slot held: %v", err)
	}
}

func (job *delayedCleanup) Usage() (execx.TreeUsage, error) { return execx.TreeUsage{}, nil }
func (job *delayedCleanup) Members() ([]execx.EndedProcess, error) {
	return nil, nil
}
func (job *delayedCleanup) TerminateAndWait(ctx context.Context) (execx.TreeUsage, error) {
	close(job.entered)
	select {
	case <-job.released:
		return execx.TreeUsage{}, nil
	case <-ctx.Done():
		return execx.TreeUsage{}, ctx.Err()
	}
}

func TestExecutorConcurrentCleanupHonorsTheWaitingCallersDeadline(t *testing.T) {
	// Arrange
	t.Setenv("CFO_VERIFY_DIR", t.TempDir())
	execution, err := BeginExecution(t.TempDir(), []string{"controlled fixture"})
	if err != nil {
		t.Fatal(err)
	}
	job := &delayedCleanup{entered: make(chan struct{}), released: make(chan struct{})}
	pending := &pendingCleanup{tree: job, execution: execution, isFinished: true, isGuardComplete: true, isTurnReleased: true, exitCode: -1}
	executor := Executor{pending: map[string]*pendingCleanup{execution.ID(): pending}}
	finished := make(chan error, 1)
	go func() { finished <- executor.RetryCleanup(context.Background()) }()
	t.Cleanup(func() {
		close(job.released)
		if err := <-finished; err != nil {
			t.Error(err)
		}
	})
	select {
	case <-job.entered:
	case <-time.After(time.Second):
		t.Fatal("first cleanup did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	// Act
	started := time.Now()
	err = executor.RetryCleanup(ctx)

	// Assert
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > time.Second {
		t.Fatalf("concurrent cleanup ignored deadline: %v after %s", err, time.Since(started))
	}
}

func TestExecutorCleanupDeadlineCoversCustodyReleaseAndEvidence(t *testing.T) {
	for _, phase := range []string{"custody", "release", "evidence"} {
		t.Run(phase, func(t *testing.T) {
			// Arrange
			t.Setenv("CFO_VERIFY_DIR", t.TempDir())
			execution, err := BeginExecution(t.TempDir(), []string{"controlled fixture"})
			if err != nil {
				t.Fatal(err)
			}
			pending := &pendingCleanup{execution: execution, usage: &execx.TreeUsage{}, isFinished: true, isGuardComplete: true, isTurnReleased: true, exitCode: -1}
			var unblock func()
			if phase == "evidence" {
				path, err := windows.UTF16PtrFromString(execution.Path())
				if err != nil {
					t.Fatal(err)
				}
				handle, err := windows.CreateFile(path, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
				if err != nil {
					t.Fatal(err)
				}
				unblock = func() { _ = windows.CloseHandle(handle) }
			} else {
				requested := admission(t)
				pending.turn, err = requested.Wait(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				pending.isTurnReleased = false
				if phase == "custody" {
					pending.guard, err = pending.turn.Protect(execution.ID(), execution.Path())
					if err != nil {
						t.Fatal(err)
					}
					pending.isGuardComplete = false
				}
				var unlock func() error
				unlock, err = custodyMutation(requested.Dir)
				if err != nil {
					t.Fatal(err)
				}
				unblock = func() {
					if err := unlock(); err != nil {
						t.Error(err)
					}
				}
			}
			defer func() {
				if unblock != nil {
					unblock()
				}
			}()
			executor := Executor{pending: map[string]*pendingCleanup{execution.ID(): pending}}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()

			// Act
			started := time.Now()
			err = executor.RetryCleanup(ctx)
			elapsed := time.Since(started)
			unblock()
			unblock = nil

			// Assert
			if !errors.Is(err, context.DeadlineExceeded) || elapsed > time.Second || len(executor.pending) != 1 {
				t.Errorf("%s ignored cleanup deadline or lost ownership: %v after %s, pending=%d", phase, err, elapsed, len(executor.pending))
			}
			if err := executor.RetryCleanup(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(executor.pending) != 0 {
				t.Fatal("successful retry retained cleanup")
			}
		})
	}
}

func TestCustodyMutationRetriesItsFailedRelease(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	unlock, err := custodyMutation(dir)
	if err != nil {
		t.Fatal(err)
	}
	path, err := windows.UTF16PtrFromString(filepath.Join(dir, "custody-mutation"))
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(path, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}

	// Act
	if err := unlock(); err == nil {
		t.Fatal("blocked mutation release was ignored")
	}
	if err := windows.CloseHandle(handle); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	nextUnlock, err := custodyMutationContext(ctx, dir)

	// Assert
	if err != nil {
		t.Fatalf("failed release poisoned future mutation: %v", err)
	}
	if err := nextUnlock(); err != nil {
		t.Fatal(err)
	}
}

func TestExecutorNestedCleanupDeadlineCoversProgressCard(t *testing.T) {
	t.Setenv("CFO_VERIFY_DIR", t.TempDir())
	requested := admission(t)
	outer, err := requested.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer outer.Release()
	t.Setenv(TurnVariable, outer.Token())
	inner, err := requested.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	execution, err := BeginExecution(t.TempDir(), []string{"controlled fixture"})
	if err != nil {
		t.Fatal(err)
	}
	pending := &pendingCleanup{execution: execution, turn: inner, usage: &execx.TreeUsage{}, isFinished: true, isGuardComplete: true, exitCode: -1}
	executor := Executor{pending: map[string]*pendingCleanup{execution.ID(): pending}}
	path, err := windows.UTF16PtrFromString(filepath.Join(requested.Dir, "slot-1.run"))
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(path, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	err = executor.RetryCleanup(ctx)
	elapsed := time.Since(started)
	windows.CloseHandle(handle)
	if !errors.Is(err, context.DeadlineExceeded) || elapsed > time.Second || len(executor.pending) != 1 {
		t.Errorf("nested release ignored deadline or lost ownership: %v after %s", err, elapsed)
	}
	if err := executor.RetryCleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !requested.isHeldAs("slot-1", outer.Token()) {
		t.Fatal("nested release removed outer custody")
	}
}
