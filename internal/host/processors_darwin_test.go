package host

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/localsocket"
)

// A host told to leave cores for the Overlord's apps starts its terminal
// clamped to the utility class (terminalStart has the measurement), which ps
// reads as a lower scheduling priority than a terminal's that is not, and the
// terminal's process is its command's own, since taskpolicy runs the command
// in its place.
func TestAMacHostToldToLeaveCoresForAppsRunsItsTerminalInTheUtilityClass(t *testing.T) {
	priorities := map[bool]int{}
	for _, shouldLeaveCoresForApps := range []bool{true, false} {
		// Arrange
		stateDir := t.TempDir()
		t.Cleanup(func() { _ = os.Remove(localsocket.Folder(stateDir)) })
		spec := Spec{ID: hostID, Args: []string{"sleep", "600"}, Cols: 80, Rows: 24, ShouldLeaveCoresForApps: shouldLeaveCoresForApps}

		// Act
		record, err := Launch(stateDir, []string{os.Args[0]}, hostEnvironment(), spec)
		if err != nil {
			t.Fatalf("Launch: %v", err)
		}
		closeWhenDone(t, stateDir, record)
		output, err := exec.Command("ps", "-o", "comm=,pri=", "-p", strconv.Itoa(record.ChildPID)).Output()
		if err != nil {
			t.Fatalf("ps of the terminal's process %d: %v", record.ChildPID, err)
		}

		// Assert
		fields := strings.Fields(string(output))
		if len(fields) != 2 || filepath.Base(fields[0]) != "sleep" {
			t.Fatalf("ps reads the terminal's process %d as %q, want sleep and its priority", record.ChildPID, output)
		}
		priority, err := strconv.Atoi(fields[1])
		if err != nil {
			t.Fatalf("ps reads the terminal's priority as %q: %v", fields[1], err)
		}
		priorities[shouldLeaveCoresForApps] = priority
	}
	if priorities[true] >= priorities[false] {
		t.Fatalf("a terminal told to leave cores for apps runs at priority %d, one that is not at %d; want it lower", priorities[true], priorities[false])
	}
}
