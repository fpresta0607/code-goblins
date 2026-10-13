package verify

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/proc"
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
			_ = os.WriteFile("child.pid", []byte(strconv.Itoa(os.Getpid())), 0600)
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
	executor := Executor{Admission: admission(t), Monitor: Monitor{Poll: time.Millisecond, Grace: time.Millisecond,
		Headroom: func() (Headroom, error) {
			if isParentGone {
				return Headroom{PhysicalAvailable: 3 << 30, CommitAvailable: 8 << 30}, nil
			}
			return Headroom{PhysicalAvailable: 8 << 30, CommitAvailable: 8 << 30}, nil
		}, Observe: func(measurement Measurement) error {
			_, err := os.Stat(filepath.Join(root, "child.pid"))
			if err == nil && measurement.Usage.TotalProcesses == 2 && measurement.Usage.ActiveProcesses == 1 {
				isParentGone = true
			}
			return nil
		}}}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Act
	result, err := executor.Run(ctx, execx.Request{Dir: root, Name: os.Args[0], Args: []string{"-test.run=^TestExecutorTracksDescendantsAfterTheParentExits$"}, Env: append(os.Environ(), roleVariable+"=parent")})

	// Assert
	if err == nil || !isParentGone || result.ExitCode != 0 {
		t.Fatalf("parent result = %+v, %v, parent gone = %v", result, err, isParentGone)
	}
	data, readError := os.ReadFile(filepath.Join(root, "child.pid"))
	if readError != nil {
		t.Fatal(readError)
	}
	childPID, parseError := strconv.Atoi(string(data))
	if parseError != nil {
		t.Fatal(parseError)
	}
	if _, isRunning := proc.StartTime(childPID); isRunning {
		t.Fatalf("owned child %d survived verified cleanup", childPID)
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
	if record.Status != "not_started" || record.ExitCode != nil || len(record.Transitions) != 2 {
		t.Fatalf("no-turn evidence = %+v", record)
	}
}
