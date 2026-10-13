package verify

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
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
		t.Fatalf("a command that passed and left a child = %+v, %v", result, err)
	}
	childPID, err := strconv.Atoi(strings.TrimSpace(string(waitForFile(t, filepath.Join(root, "child.pid"), time.Second))))
	if err != nil {
		t.Fatal(err)
	}
	if child, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(childPID)); err == nil {
		isEnded := hasEnded(child)
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
		time.Sleep(time.Second)
		release.Close()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Act
	result, record, err := executor.RunWithin(ctx, execx.Request{Dir: root, Name: program, Env: append(os.Environ(), standin.Env("", standin.Rule{Any: true, Stdout: "ran", Exit: 0})...)}, 500*time.Millisecond)

	// Assert
	if err != nil || result.ExitCode != 0 || string(result.Stdout) != "ran" {
		t.Fatalf("a quick command behind a longer wait = %+v, %v", result, err)
	}
	if evidence := readExecution(t, record); evidence.Status != "passed" || evidence.QueueSeconds < 0.5 {
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
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 15*time.Second {
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
