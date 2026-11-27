package host

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/localsocket"
)

// utilityPriority is the scheduling priority macOS gives a program in the
// utility class; a default program's, 31, is above it.
const utilityPriority = 20

// ownPriority is this test process's scheduling priority, and that priority
// with its nice value as ps reads them, to name in a failure.
func ownPriority(t *testing.T) (int, string) {
	t.Helper()
	output, err := exec.Command("ps", "-o", "pri=,nice=", "-p", strconv.Itoa(os.Getpid())).Output()
	fields := strings.Fields(string(output))
	if err != nil || len(fields) != 2 {
		t.Fatalf("ps reads this test's process as %q (%v), want its priority and nice value", output, err)
	}
	priority, err := strconv.Atoi(fields[0])
	if err != nil {
		t.Fatalf("ps reads this test's priority as %q: %v", fields[0], err)
	}
	return priority, "priority " + fields[0] + ", nice " + fields[1]
}

// processPriority waits up to 30 seconds for ps to read the process pid as
// the command name, since taskpolicy and a forked shell each run their
// command a moment after the process starts, and returns its scheduling
// priority.
func processPriority(t *testing.T, pid int, name string) int {
	t.Helper()
	var fields []string
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		output, err := exec.Command("ps", "-o", "comm=,pri=", "-p", strconv.Itoa(pid)).Output()
		fields = strings.Fields(string(output))
		if err == nil && len(fields) == 2 && filepath.Base(fields[0]) == name {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ps reads the process %d as %q (%v), want %s and its priority", pid, output, err, name)
		}
	}
	priority, err := strconv.Atoi(fields[1])
	if err != nil {
		t.Fatalf("ps reads the priority of the process %d as %q: %v", pid, fields[1], err)
	}
	return priority
}

// childProcess waits up to 30 seconds for pgrep to find a child of the
// process pid and returns it.
func childProcess(t *testing.T, pid int) int {
	t.Helper()
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		output, err := exec.Command("pgrep", "-P", strconv.Itoa(pid)).Output()
		if fields := strings.Fields(string(output)); err == nil && len(fields) == 1 {
			child, err := strconv.Atoi(fields[0])
			if err != nil {
				t.Fatalf("pgrep reads a child of the process %d as %q: %v", pid, fields[0], err)
			}
			return child
		}
		if time.Now().After(deadline) {
			t.Fatalf("pgrep finds the children %q of the process %d (%v), want one", output, pid, err)
		}
	}
}

// A host told to leave cores for the Overlord's apps starts its terminal
// clamped to the utility class (terminalStart). The terminal's process is its
// command's own, sh, since taskpolicy runs the command in its place, and the
// sleep sh starts keeps the clamp, which ps reads as the utility class's
// priority or lower. Where this test runs above that priority, as a default
// program does, the clamped sleep runs lower than one an unclamped terminal
// starts. GitHub's macOS runner runs its jobs at the utility class's priority
// already, so there the two cannot be told apart, and the test says so.
func TestAMacHostToldToLeaveCoresForAppsRunsItsTerminalInTheUtilityClass(t *testing.T) {
	own, runner := ownPriority(t)
	priorities := map[bool]int{}
	for _, shouldLeaveCoresForApps := range []bool{true, false} {
		// Arrange
		stateDir := t.TempDir()
		t.Cleanup(func() { _ = os.Remove(localsocket.Folder(stateDir)) })
		spec := Spec{ID: hostID, Args: []string{"sh", "-c", "sleep 600 & wait"}, Cols: 80, Rows: 24, ShouldLeaveCoresForApps: shouldLeaveCoresForApps}

		// Act
		record, err := Launch(stateDir, []string{os.Args[0]}, hostEnvironment(), spec)
		if err != nil {
			t.Fatalf("Launch: %v", err)
		}
		closeWhenDone(t, stateDir, record)

		// Assert
		processPriority(t, record.ChildPID, "sh")
		priorities[shouldLeaveCoresForApps] = processPriority(t, childProcess(t, record.ChildPID), "sleep")
	}
	if priorities[true] > utilityPriority {
		t.Fatalf("a sleep the terminal told to leave cores for apps starts runs at priority %d, want the utility class's %d or lower; this test runs at %s", priorities[true], utilityPriority, runner)
	}
	if own <= utilityPriority {
		t.Logf("this test runs at %s, at or below the utility class's priority, %d, so the clamped sleep (%d) cannot be told from the unclamped one (%d) here", runner, utilityPriority, priorities[true], priorities[false])
		return
	}
	if priorities[true] >= priorities[false] {
		t.Fatalf("a sleep the terminal told to leave cores for apps starts runs at priority %d, one the other terminal starts at %d; want it lower; this test runs at %s", priorities[true], priorities[false], runner)
	}
}
