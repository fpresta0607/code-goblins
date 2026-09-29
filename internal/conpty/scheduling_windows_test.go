package conpty

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/proc"
)

func readScheduling(t *testing.T, process windows.Handle) (uint32, uint32) {
	t.Helper()
	state := struct{ Version, Control, State uint32 }{Version: 1}
	result, _, err := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetProcessInformation").Call(
		uintptr(process), 4, uintptr(unsafe.Pointer(&state)), unsafe.Sizeof(state))
	if result == 0 {
		t.Fatal(err)
	}
	return state.Control, state.State
}

func assertInteractiveScheduling(t *testing.T, pid int) {
	t.Helper()
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(process)
	control, state := readScheduling(t, process)
	if control&1 == 0 || state&1 != 0 {
		t.Errorf("pid %d uses automatic/throttled scheduling: control=%#x state=%#x", pid, control, state)
	}
}

func TestConsoleKeepsHostConsoleAndDescendantsInteractive(t *testing.T) {
	before, err := proc.Processes()
	if err != nil {
		t.Fatal(err)
	}
	previous := make(map[int]bool, len(before))
	for _, process := range before {
		previous[process.PID] = true
	}
	console, output := startChild(t, Spec{Cols: 80, Rows: 25,
		Args: []string{filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe"), "/d", "/s", "/c", windows.ComposeCommandLine([]string{os.Args[0]})},
	})

	assertInteractiveScheduling(t, os.Getpid())
	assertInteractiveScheduling(t, console.PID())
	processes, err := proc.Processes()
	if err != nil {
		t.Fatal(err)
	}
	isConsoleFound := false
	for _, process := range processes {
		if process.ParentPID == console.PID() {
			assertInteractiveScheduling(t, process.PID)
		}
		if !previous[process.PID] && process.ParentPID == os.Getpid() && strings.EqualFold(process.ExeBase, "conhost.exe") {
			isConsoleFound = true
			assertInteractiveScheduling(t, process.PID)
		}
	}
	if !isConsoleFound {
		t.Fatal("new pseudo console has no console server")
	}

	typeLine(t, console, "spawn-attached")
	match := output.waitFor(t, `grandchild (\d+)`)
	pid, err := strconv.Atoi(match[1])
	if err != nil {
		t.Fatal(err)
	}
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(process)
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		control, state := readScheduling(t, process)
		if control&1 != 0 && state&1 == 0 {
			return
		}
	}
	assertInteractiveScheduling(t, pid)
}

func writeScheduling(t *testing.T, process windows.Handle, control, state uint32) {
	t.Helper()
	policy := powerThrottling{Version: 1, ControlMask: control, StateMask: state}
	result, _, err := setProcessInformation.Call(uintptr(process), PROCESS_POWER_THROTTLING, uintptr(unsafe.Pointer(&policy)), unsafe.Sizeof(policy))
	if result == 0 {
		t.Fatal(err)
	}
}

func TestInteractiveSchedulingPreservesOtherPowerControls(t *testing.T) {
	console, _ := startChild(t, Spec{Cols: 80, Rows: 25})
	// IGNORE_TIMER_RESOLUTION is independent of execution-speed throttling.
	writeScheduling(t, console.process, 5, 5)

	if err := interactiveScheduling(console.process); err != nil {
		t.Fatal(err)
	}

	control, state := readScheduling(t, console.process)
	if control != 5 || state != 4 {
		t.Fatalf("power controls changed: control=%#x state=%#x, want 5/4", control, state)
	}
}

func TestJobSchedulingPreservesExplicitDescendantPowerPolicy(t *testing.T) {
	console, _ := startChild(t, Spec{Cols: 80, Rows: 25})
	writeScheduling(t, console.process, 1, 1)

	if err := console.scheduling.scheduleProcess(uint32(console.PID())); err != nil {
		t.Fatal(err)
	}

	control, state := readScheduling(t, console.process)
	if control != 1 || state != 1 {
		t.Fatalf("explicit EcoQoS was overwritten: control=%#x state=%#x", control, state)
	}
}

func TestJobSchedulingReconcilesAutomaticPolicyBeforeInput(t *testing.T) {
	console, output := startChild(t, Spec{Cols: 80, Rows: 25})
	typeLine(t, console, "first")
	output.waitFor(t, "got first")
	writeScheduling(t, console.process, 0, 0)
	console.scheduling.mu.Lock()
	console.scheduling.lastReconciled = time.Time{}
	console.scheduling.mu.Unlock()

	typeLine(t, console, "reconciled")

	assertInteractiveScheduling(t, console.PID())
	output.waitFor(t, "got reconciled")
}

func TestJobSchedulingDoesNotChangeAnotherProcess(t *testing.T) {
	console, _ := startChild(t, Spec{Cols: 80, Rows: 25})
	other := exec.Command(os.Args[0])
	other.Env = append(os.Environ(), childMode+"=sleep")
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Process.Kill(); _ = other.Wait() })
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(other.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(process)
	controlBefore, stateBefore := readScheduling(t, process)

	if err := console.scheduling.scheduleProcess(uint32(other.Process.Pid)); err != nil {
		t.Fatal(err)
	}

	control, state := readScheduling(t, process)
	if control != controlBefore || state != stateBefore {
		t.Fatalf("nonmember power policy changed from %d/%d to %d/%d", controlBefore, stateBefore, control, state)
	}
}

func TestInteractiveSchedulingReportsInvalidHandle(t *testing.T) {
	if err := interactiveScheduling(0); err == nil {
		t.Fatal("invalid process handle accepted")
	}
}

func TestSchedulingFailureDoesNotLoseTerminalInput(t *testing.T) {
	console, output := startChild(t, Spec{Cols: 80, Rows: 25})
	scheduling := console.scheduling
	console.scheduling = &jobScheduling{job: windows.InvalidHandle}
	t.Cleanup(func() { console.scheduling = scheduling })

	typeLine(t, console, "input survives scheduling failure")

	output.waitFor(t, "got input survives scheduling failure")
}

func TestInputReconciliationWithManyJobMembers(t *testing.T) {
	console, output := startChild(t, Spec{Cols: 80, Rows: 25})
	for range 12 {
		typeLine(t, console, "spawn-attached")
	}
	typeLine(t, console, "all children started")
	output.waitFor(t, "got all children started")
	var samples []time.Duration

	for range 40 {
		console.scheduling.mu.Lock()
		console.scheduling.lastReconciled = time.Time{}
		console.scheduling.mu.Unlock()
		started := time.Now()
		typeLine(t, console, "measured")
		samples = append(samples, time.Since(started))
	}

	slices.Sort(samples)
	t.Logf("13-member input reconciliation p95=%s max=%s", samples[37], samples[39])
	if samples[37] >= 50*time.Millisecond || samples[39] > 250*time.Millisecond {
		t.Fatalf("input reconciliation exceeded latency bounds: %v", samples)
	}
	output.waitFor(t, "got measured")
}

func TestNewConsoleDoesNotChangeExistingConsoleScheduling(t *testing.T) {
	first, _ := startChild(t, Spec{Cols: 80, Rows: 25})
	processes, err := proc.Processes()
	if err != nil {
		t.Fatal(err)
	}
	var handles []windows.Handle
	for _, process := range processes {
		if process.ParentPID != os.Getpid() || !strings.EqualFold(process.ExeBase, "conhost.exe") {
			continue
		}
		handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_SET_INFORMATION, false, uint32(process.PID))
		if err != nil {
			t.Fatal(err)
		}
		defer windows.CloseHandle(handle)
		writeScheduling(t, handle, 1, 1)
		handles = append(handles, handle)
	}
	if len(handles) == 0 {
		t.Fatal("first console has no server")
	}

	second, _ := startChild(t, Spec{Cols: 80, Rows: 25})

	for _, handle := range handles {
		control, state := readScheduling(t, handle)
		if control != 1 || state != 1 {
			t.Errorf("an existing console was changed to %d/%d", control, state)
		}
		writeScheduling(t, handle, 1, 0)
	}
	if second.PID() == first.PID() {
		t.Fatal("consoles share a process")
	}
}
