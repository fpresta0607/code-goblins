//go:build linux || darwin

package verify

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

func TestRecoveryEndsTheTreeOfAKilledControllerAndFreesTheTurn(t *testing.T) {
	switch os.Getenv(recoveryRole) {
	case "controller":
		os.Exit(controlAnExecution())
	case "worker":
		// The worker starts its own worker and exits first, so the tree
		// holds a process whose parent has gone.
		child := execx.Command(os.Args[0], os.Args[1:]...)
		child.Env = append(os.Environ(), recoveryRole+"=descendant")
		if err := child.Start(); err != nil {
			os.Exit(8)
		}
		os.Exit(0)
	case "descendant":
		if err := os.WriteFile("descendant.pid.tmp", []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil || os.Rename("descendant.pid.tmp", "descendant.pid") != nil {
			os.Exit(7)
		}
		time.Sleep(time.Minute)
		os.Exit(9)
	}
	// Arrange
	store := t.TempDir()
	t.Setenv("GOBLINS_VERIFY_DIR", store)
	root := t.TempDir()
	requested := admission(t)
	controller := exec.Command(os.Args[0], "-test.run=^TestRecoveryEndsTheTreeOfAKilledControllerAndFreesTheTurn$")
	controller.Env = append(os.Environ(), recoveryRole+"=controller", "VERIFY_RECOVERY_ADMISSION="+requested.Dir, "VERIFY_RECOVERY_ROOT="+root)
	output, err := os.Create(filepath.Join(t.TempDir(), "controller.txt"))
	if err != nil {
		t.Fatal(err)
	}
	controller.Stdout, controller.Stderr = output, output
	err = controller.Start()
	output.Close()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = controller.Process.Kill(); _ = controller.Wait() })
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		_ = controller.Process.Kill()
		_ = controller.Wait()
		printed, _ := os.ReadFile(output.Name())
		t.Logf("the controller printed:\n%s", printed)
		records, _ := filepath.Glob(filepath.Join(store, "executions", "*.json"))
		for _, record := range records {
			content, _ := os.ReadFile(record)
			t.Logf("%s:\n%s", record, content)
		}
	})
	descendantPID, err := strconv.Atoi(strings.TrimSpace(string(waitForFile(t, filepath.Join(root, "descendant.pid"), time.Minute))))
	if err != nil {
		t.Fatal(err)
	}
	descendant, err := proc.Lookup(descendantPID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = proc.Signal(descendant.PID, descendant.Start, syscall.SIGKILL) })
	records, err := filepath.Glob(filepath.Join(requested.Dir, "custody", "*.json"))
	if err != nil || len(records) != 1 {
		t.Fatalf("custody = %v, %v", records, err)
	}
	runID := strings.TrimSuffix(filepath.Base(records[0]), ".json")
	if err := controller.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = controller.Wait()
	if _, err := proc.Lookup(descendant.PID); err != nil {
		t.Fatalf("the fixture's descendant ended with its controller, so it proves nothing: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	// Act
	turn, err := requested.Wait(ctx)
	defer turn.Release()

	// Assert
	if err != nil {
		t.Fatalf("an abandoned execution kept the machine's turn: %v", err)
	}
	if !isGone(descendant.PID) {
		t.Fatal("the turn was given while the abandoned execution's descendant still ran")
	}
	if left, _ := filepath.Glob(filepath.Join(requested.Dir, "custody", "*.json")); len(left) != 0 {
		t.Fatalf("custody left after recovery: %v", left)
	}
	record := readExecution(t, filepath.Join(store, "executions", runID+".json"))
	if record.Status != "failed" || record.Measurement.State != Interrupted || !record.Measurement.CleanupConfirmed || record.Measurement.Usage.ActiveProcesses != 0 || !strings.Contains(record.Failure, "recovered") {
		t.Fatalf("recovered evidence = %+v", record)
	}
}
