//go:build linux || darwin

package execx

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/proc"
)

// startLeaving starts script under sh as a tree, in a process group of its
// own when isGrouped, waits for sh to exit, and returns the tree and the pid
// of the helper the script left running, which it writes to its output.
func startLeaving(t *testing.T, script string, isGrouped bool, start func(*exec.Cmd) (*Tree, error)) (*Tree, int) {
	t.Helper()
	output := filepath.Join(t.TempDir(), "helper.pid")
	cmd := Command("sh", "-c", script+" > /dev/null 2>&1 & echo $! > "+output)
	if isGrouped {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	tree, err := start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	helper, err := strconv.Atoi(strings.TrimSpace(string(written)))
	if err != nil {
		t.Fatal(err)
	}
	// What a failed test leaves is ended by its pid while it is the helper.
	if entry, err := proc.Lookup(helper); err == nil {
		t.Cleanup(func() { _ = proc.Signal(helper, entry.Start, syscall.SIGKILL) })
	}
	return tree, helper
}

// awaitGone waits until pid no longer names a running process.
func awaitGone(t *testing.T, pid int) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if _, err := proc.Lookup(pid); errors.Is(err, fs.ErrNotExist) {
			return
		}
	}
	t.Fatalf("process %d still runs", pid)
}

// awaitWritten waits until a helper has written path.
func awaitWritten(t *testing.T, path string) {
	t.Helper()
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("a helper did not write %s within a minute", path)
		}
	}
}

func isAmong(processes []EndedProcess, pid int) bool {
	return slices.ContainsFunc(processes, func(process EndedProcess) bool { return process.PID == pid })
}

func TestEndEndsAHelperWhoseParentExitedAndNothingOutsideTheTree(t *testing.T) {
	// Arrange
	outside := exec.Command("sleep", "600")
	if err := outside.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = outside.Process.Kill(); _ = outside.Wait() })
	tree, helper := startLeaving(t, "sleep 600", false, StartTree)

	// Act
	members, membersErr := tree.Members()
	ended, err := tree.End()

	// Assert
	if membersErr != nil || err != nil {
		t.Fatal(membersErr, err)
	}
	if !isAmong(members, helper) || !isAmong(ended, helper) {
		t.Fatalf("the tree lists %v and ended %v, without the helper %d whose parent exited", members, ended, helper)
	}
	awaitGone(t, helper)
	if isAmong(ended, outside.Process.Pid) {
		t.Fatalf("the tree's end ended %v, a process started outside it among them", ended)
	}
	if _, err := proc.Lookup(outside.Process.Pid); err != nil {
		t.Fatalf("a process started outside the tree no longer runs: %v", err)
	}
}

func TestATreeInAGroupOfItsOwnFindsAHelperThatClearedItsEnvironment(t *testing.T) {
	// Arrange: env -i starts the helper with no mark, and its parent exits,
	// so only the group still finds it.
	tree, helper := startLeaving(t, "env -i sleep 600", true, StartTree)

	// Act
	members, err := tree.Members()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if !isAmong(members, helper) {
		t.Fatalf("the tree lists %v, without the helper %d in its group", members, helper)
	}
	if _, err := tree.End(); err != nil {
		t.Fatal(err)
	}
	awaitGone(t, helper)
}

// heldBytes is the memory the helper of the usage test touches.
const heldBytes = 16 << 20

func TestUsageCountsATreesProcessesAndEndAndWaitLeavesNoneRunning(t *testing.T) {
	if ready := os.Getenv("EXECX_TEST_READY"); ready != "" {
		held := make([]byte, heldBytes)
		for at := range held {
			held[at] = 1
		}
		if err := os.WriteFile(ready, nil, 0o600); err != nil {
			os.Exit(7)
		}
		time.Sleep(10 * time.Minute)
		runtime.KeepAlive(held)
		os.Exit(9)
	}
	// Arrange: the helper is this binary holding memory it touched, as
	// Linux may read a process as small as sleep to hold none yet.
	ready := filepath.Join(t.TempDir(), "ready")
	tree, helper := startLeaving(t, fmt.Sprintf("EXECX_TEST_READY='%s' '%s' -test.run='^TestUsageCountsATreesProcessesAndEndAndWaitLeavesNoneRunning$'", ready, os.Args[0]), true, StartTree)
	awaitWritten(t, ready)

	// Act
	running, err := tree.Usage()
	if err != nil {
		t.Fatal(err)
	}
	ended, spared, err := tree.EndAndWait(context.Background())

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if running.ActiveProcesses != 1 || running.TotalProcesses != 1 || running.CommitBytes < heldBytes/2 || running.PeakCommitBytes < running.CommitBytes {
		t.Fatalf("usage of a tree running only its helper = %+v", running)
	}
	if ended.ActiveProcesses != 0 || ended.TotalProcesses != 1 || ended.PeakCommitBytes != running.PeakCommitBytes || len(spared) != 0 {
		t.Fatalf("EndAndWait = %+v, sparing %v", ended, spared)
	}
	awaitGone(t, helper)
	if _, err := tree.Usage(); err == nil {
		t.Fatal("a tree read after its end reported its usage")
	}
}

func TestANamedTreeIsFoundAgainByItsNameOnceItsStarterLetItGo(t *testing.T) {
	// Arrange
	name := fmt.Sprintf("goblins-verify-test-%d-%d", os.Getpid(), time.Now().UnixNano())
	tree, helper := startLeaving(t, "sleep 600", false, func(cmd *exec.Cmd) (*Tree, error) { return StartNamedTree(cmd, name) })
	if err := tree.Release(); err != nil {
		t.Fatal(err)
	}

	// Act
	_, refused := StartNamedTree(Command("true"), name)
	usage, isFound, err := EndNamedJob(context.Background(), name)
	_, isFoundAgain, againErr := EndNamedJob(context.Background(), name)

	// Assert
	if refused == nil {
		t.Error("a tree started under a name a running process carries")
	}
	if err != nil || !isFound || usage.ActiveProcesses != 0 || usage.TotalProcesses != 1 {
		t.Fatalf("EndNamedJob = %+v, found %t, %v", usage, isFound, err)
	}
	awaitGone(t, helper)
	if againErr != nil || isFoundAgain {
		t.Fatalf("EndNamedJob of an ended tree = found %t, %v", isFoundAgain, againErr)
	}
}

func TestEndAndWaitAsksEachProcessToEndOnlyOnce(t *testing.T) {
	if cleaned := os.Getenv("EXECX_TEST_CLEANED"); cleaned != "" {
		terminate := make(chan os.Signal, 1)
		signal.Notify(terminate, syscall.SIGTERM)
		if err := os.WriteFile(cleaned+".ready", nil, 0o600); err != nil {
			os.Exit(7)
		}
		<-terminate
		signal.Reset(syscall.SIGTERM)
		time.Sleep(300 * time.Millisecond)
		if err := os.WriteFile(cleaned, nil, 0o600); err != nil {
			os.Exit(7)
		}
		os.Exit(0)
	}
	// Arrange: the helper cleans up for 300ms on its first SIGTERM and ends
	// at once on a second, as a program using signal.NotifyContext does once
	// it stops.
	cleaned := filepath.Join(t.TempDir(), "cleaned")
	tree, helper := startLeaving(t, fmt.Sprintf("EXECX_TEST_CLEANED='%s' '%s' -test.run='^TestEndAndWaitAsksEachProcessToEndOnlyOnce$'", cleaned, os.Args[0]), false, StartTree)
	awaitWritten(t, cleaned+".ready")

	// Act
	_, spared, err := tree.EndAndWait(context.Background())

	// Assert
	if err != nil || len(spared) != 0 {
		t.Fatalf("EndAndWait spared %v: %v", spared, err)
	}
	if _, err := os.Stat(cleaned); err != nil {
		t.Fatalf("the helper was ended before it cleaned up, so it was asked to end more than once: %v", err)
	}
	awaitGone(t, helper)
}

func TestATreeStartedInsideAnotherStaysInBoth(t *testing.T) {
	// Arrange: the command starts inside a tree named outer, as a step under
	// the gate's executor does, and its parent exits, so only the marks find
	// the helper.
	outer := fmt.Sprintf("goblins-outer-test-%d-%d", os.Getpid(), time.Now().UnixNano())
	tree, helper := startLeaving(t, "sleep 600", false, func(cmd *exec.Cmd) (*Tree, error) {
		cmd.Env = append(cmd.Environ(), TreeVariable+"="+outer)
		return StartTree(cmd)
	})

	// Act
	members, membersErr := tree.Members()
	_, isPrefixFound, prefixErr := EndNamedJob(context.Background(), outer[:len(outer)-1])
	usage, isFound, err := EndNamedJob(context.Background(), outer)

	// Assert
	if membersErr != nil || !isAmong(members, helper) {
		t.Fatalf("the inner tree lists %v, without the helper %d: %v", members, helper, membersErr)
	}
	if prefixErr != nil || isPrefixFound {
		t.Fatalf("EndNamedJob of a name the helper carries only part of = found %t, %v", isPrefixFound, prefixErr)
	}
	if err != nil || !isFound || usage.TotalProcesses != 1 {
		t.Fatalf("EndNamedJob of the outer tree = %+v, found %t, %v", usage, isFound, err)
	}
	awaitGone(t, helper)
}
