package monitor

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// A status call that exits but leaves a process holding its output pipe held
// the probe, and the supervisor loop waiting on it, until that process ended.
func TestExecGateProberReturnsWhenTheStatusCallLeavesAProcessHoldingItsOutput(t *testing.T) {
	fixture := newPollFixture(t)
	fixture.standIn("no-mistakes.exe")
	pidFile := filepath.Join(fixture.root, "child.pid")
	t.Setenv("PATH", fixture.bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CEO_POLL_STANDIN", "gate")
	t.Setenv("CEO_GATE_STATUS", wedgedStatus)
	t.Setenv("CEO_GATE_CHILD_PID", pidFile)

	start := time.Now()
	sample, err := ExecGateProber{}.InspectGate(context.Background(), state.TaskMeta{Worktree: fixture.dir("work")})
	elapsed := time.Since(start)

	// The process the status call left runs for a minute, so while it still
	// runs the probe did not wait for it, however slow this machine is.
	if !leftRunning(t, pidFile) {
		t.Fatalf("InspectGate took %s and returned only once the process the status call left running had ended", elapsed)
	}
	if err != nil && !sample.Active {
		t.Fatalf("InspectGate = %+v, %v; want the status it printed read", sample, err)
	}
	if !sample.Active || sample.Step != "ci" {
		t.Errorf("sample = %+v, want the active ci step the status call printed", sample)
	}
}

// A board started before the gate's home moved carries no NM_HOME of its
// own, so the status call is given the home the user's environment names,
// not no-mistakes' default.
func TestExecGateProberAsksTheGateHomeTheUserNames(t *testing.T) {
	// Arrange
	fixture := newPollFixture(t)
	fixture.standIn("no-mistakes.exe")
	said := filepath.Join(fixture.root, "nm-home.txt")
	const moved = `D:\CodeGoblins\no-mistakes`
	t.Setenv("PATH", fixture.bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CEO_POLL_STANDIN", "gate-home")
	t.Setenv("CEO_GATE_HOME_FILE", said)
	t.Setenv("NM_HOME", "")
	userEnv := filepath.Join(fixture.root, "user-env.json")
	t.Setenv("CEO_USER_ENV_FILE", userEnv)
	if err := os.WriteFile(userEnv, []byte(`{"NM_HOME": "D:\\CodeGoblins\\no-mistakes"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	_, err := ExecGateProber{}.InspectGate(context.Background(), state.TaskMeta{Worktree: fixture.dir("work")})

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(said); err != nil || string(got) != moved {
		t.Errorf("the status call ran on NM_HOME %q (%v), want %s", got, err, moved)
	}
}

// leftRunning reports whether the process whose pid the gate stand-in wrote
// to pidFile still runs, and ends it when the test ends through the handle it
// was checked by: opened again by pid once it had ended, the pid could name
// any later process.
func leftRunning(t *testing.T, pidFile string) bool {
	t.Helper()
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("the gate stand-in recorded no process it left running: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("the gate stand-in recorded %q, want a pid", data)
	}
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return false
	}
	t.Cleanup(func() {
		_ = windows.TerminateProcess(process, 1)
		_ = windows.CloseHandle(process)
	})
	var code uint32
	return windows.GetExitCodeProcess(process, &code) == nil && code == stillActive
}

// stillActive is the exit code Windows reports for a process that runs.
const stillActive = 259
