//go:build linux || darwin

package verify

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

// plentyOfMemory reads a machine that never presses a run.
func plentyOfMemory() (Headroom, error) {
	return Headroom{PhysicalAvailable: 8 << 30, CommitAvailable: 8 << 30}, nil
}

// helperOf reads the pid a run's script wrote to helper.pid in root.
func helperOf(t *testing.T, root string) int {
	t.Helper()
	written, err := os.ReadFile(filepath.Join(root, "helper.pid"))
	if err != nil {
		t.Fatal(err)
	}
	helper, err := strconv.Atoi(strings.TrimSpace(string(written)))
	if err != nil {
		t.Fatal(err)
	}
	return helper
}

// isGone reports whether pid no longer names a running process, within ten
// seconds.
func isGone(pid int) bool {
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if _, err := proc.Lookup(pid); errors.Is(err, fs.ErrNotExist) {
			return true
		}
	}
	return false
}

func TestExecutorEndsAndNamesWhatItsCommandLeftRunning(t *testing.T) {
	// Arrange
	store := t.TempDir()
	t.Setenv("GOBLINS_VERIFY_DIR", store)
	root := t.TempDir()
	executor := Executor{Admission: admission(t), Monitor: Monitor{Poll: 10 * time.Millisecond, Linger: 200 * time.Millisecond, Headroom: plentyOfMemory}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	// Act
	result, err := executor.Run(ctx, execx.Request{Dir: root, Name: "sh", Args: []string{"-c", "sleep 600 > /dev/null 2>&1 & echo $! > helper.pid; echo proof"}})

	// Assert
	if err != nil || result.ExitCode != 0 || strings.TrimSpace(string(result.Stdout)) != "proof" {
		t.Fatalf("result = %+v, %v", result, err)
	}
	helper := helperOf(t, root)
	if !isGone(helper) {
		t.Fatalf("the helper %d the command left still runs", helper)
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
	if record.Status != "passed" || !record.Measurement.CleanupConfirmed || record.Measurement.Usage.ActiveProcesses != 0 {
		t.Fatalf("evidence = %+v", record)
	}
	if !slices.ContainsFunc(record.Measurement.Leftovers, func(leftover Leftover) bool { return leftover.PID == helper }) {
		t.Fatalf("leftovers %v do not name the helper %d", record.Measurement.Leftovers, helper)
	}
	holders, waiters, err := Line(executor.Admission.Dir)
	if err != nil || len(holders)+len(waiters) != 0 {
		t.Fatalf("custody leaked: %v, %v, %v", holders, waiters, err)
	}
}

func TestExecutorInterruptsItsCommandsGroupAndEndsWhatOutlivesIt(t *testing.T) {
	// Arrange: sh answers the interrupt, and the helper it starts in the
	// background ignores it, as a background job of a script does, so only
	// the end after the grace stops it.
	if signal.Ignored(syscall.SIGINT) {
		t.Fatal("this process started with SIGINT ignored, which its commands inherit and no shell may trap, so the test would prove nothing")
	}
	t.Setenv("GOBLINS_VERIFY_DIR", t.TempDir())
	root := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	executor := Executor{Admission: admission(t), Monitor: Monitor{Poll: 10 * time.Millisecond, Grace: 500 * time.Millisecond, Headroom: plentyOfMemory,
		Observe: func(measurement Measurement) error {
			if _, err := os.Stat(filepath.Join(root, "helper.pid")); err == nil && measurement.State == Running {
				cancel()
			}
			return nil
		}}}

	// Act
	result, err := executor.Run(ctx, execx.Request{Dir: root, Name: "sh", Args: []string{"-c", `trap 'echo interrupted; exit 3' INT; sleep 600 > /dev/null 2>&1 & echo $! > helper.pid; wait`}})

	// Assert
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("a run whose context ended = %+v, %v", result, err)
	}
	if result.ExitCode != 3 || strings.TrimSpace(string(result.Stdout)) != "interrupted" {
		t.Fatalf("the command did not answer its interrupt: %+v", result)
	}
	if helper := helperOf(t, root); !isGone(helper) {
		t.Fatalf("the helper %d that ignored the interrupt still runs", helper)
	}
}
