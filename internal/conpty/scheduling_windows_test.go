package conpty

import (
	"errors"
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

func isInteractive(t *testing.T, process windows.Handle) bool {
	t.Helper()
	control, state := readScheduling(t, process)
	return control&1 != 0 && state&1 == 0
}

func assertInteractiveScheduling(t *testing.T, process windows.Handle) {
	t.Helper()
	if !isInteractive(t, process) {
		control, state := readScheduling(t, process)
		t.Errorf("process uses automatic/throttled scheduling: control=%#x state=%#x", control, state)
	}
}

// waitInteractiveScheduling polls one retained handle, since the job's
// notifications schedule a process a wrapper starts asynchronously.
func waitInteractiveScheduling(t *testing.T, process windows.Handle) {
	t.Helper()
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		if isInteractive(t, process) {
			return
		}
	}
	assertInteractiveScheduling(t, process)
}

// openProcess opens pid for the rest of the test, so its pid cannot be
// reused while the test holds it.
func openProcess(t *testing.T, pid int) windows.Handle {
	t.Helper()
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_SET_INFORMATION, false, uint32(pid))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { windows.CloseHandle(process) })
	return process
}

// consoleServers lists this process's direct conhost children, which include
// servers of earlier tests' consoles that are still exiting.
func consoleServers(t *testing.T) []int {
	t.Helper()
	processes, err := proc.Processes()
	if err != nil {
		t.Fatal(err)
	}
	var servers []int
	for _, process := range processes {
		if process.ParentPID == os.Getpid() && strings.EqualFold(process.ExeBase, "conhost.exe") {
			servers = append(servers, process.PID)
		}
	}
	return servers
}

// startChildWithServer starts a console and returns a handle on the console
// server it created. Handles held on the servers that already existed keep
// their pids from being reused, so neither an exiting older server nor a
// recycled pid is taken for the new one.
func startChildWithServer(t *testing.T, spec Spec) (*Console, *screen, windows.Handle) {
	t.Helper()
	existing := map[int]bool{}
	for _, pid := range consoleServers(t) {
		process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			continue // Already gone, so its pid is free for the new server.
		}
		if err != nil {
			t.Fatal(err)
		}
		defer windows.CloseHandle(process)
		existing[pid] = true
	}
	console, output := startChild(t, spec)
	var server windows.Handle
	for _, pid := range consoleServers(t) {
		if existing[pid] {
			continue
		}
		if server != 0 {
			t.Fatal("the new pseudo console has more than one console server")
		}
		server = openProcess(t, pid)
	}
	if server == 0 {
		t.Fatal("the new pseudo console has no console server")
	}
	return console, output, server
}

func TestConsoleKeepsHostConsoleAndDescendantsInteractive(t *testing.T) {
	console, output, server := startChildWithServer(t, Spec{Cols: 80, Rows: 25,
		Args: []string{filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe"), "/d", "/s", "/c", windows.ComposeCommandLine([]string{os.Args[0]})},
	})

	assertInteractiveScheduling(t, windows.CurrentProcess())
	assertInteractiveScheduling(t, console.process)
	assertInteractiveScheduling(t, server)
	typeLine(t, console, "pid")
	child, err := strconv.Atoi(output.waitFor(t, `pid (\d+)`)[1])
	if err != nil {
		t.Fatal(err)
	}
	if child == console.PID() {
		t.Fatal("the console runs the child directly, not through the cmd wrapper")
	}
	waitInteractiveScheduling(t, openProcess(t, child))

	typeLine(t, console, "spawn-attached")
	grandchild, err := strconv.Atoi(output.waitFor(t, `grandchild (\d+)`)[1])
	if err != nil {
		t.Fatal(err)
	}
	waitInteractiveScheduling(t, openProcess(t, grandchild))
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

	assertInteractiveScheduling(t, console.process)
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
	first, _, server := startChildWithServer(t, Spec{Cols: 80, Rows: 25})
	originalControl, originalState := readScheduling(t, server)
	// Registered before the second console starts, so a failure restores too.
	t.Cleanup(func() { writeScheduling(t, server, originalControl, originalState) })
	writeScheduling(t, server, 1, 1)

	second, _ := startChild(t, Spec{Cols: 80, Rows: 25})

	if control, state := readScheduling(t, server); control != 1 || state != 1 {
		t.Errorf("the existing console server was changed to %d/%d", control, state)
	}
	if second.PID() == first.PID() {
		t.Fatal("consoles share a process")
	}
}
