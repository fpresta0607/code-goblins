package main

import (
	"bytes"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// powerPolicy is PROCESS_POWER_THROTTLING_STATE.
type powerPolicy struct {
	Version, ControlMask, StateMask uint32
}

// processPowerThrottling is the ProcessPowerThrottling information class, and
// executionSpeed its execution speed bit.
const (
	processPowerThrottling = 4
	executionSpeed         = 1
)

var (
	getProcessInformation = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetProcessInformation")
	setProcessInformation = windows.NewLazySystemDLL("kernel32.dll").NewProc("SetProcessInformation")
)

func currentPowerPolicy(t *testing.T) powerPolicy {
	t.Helper()
	policy := powerPolicy{Version: 1}
	if result, _, err := getProcessInformation.Call(uintptr(windows.CurrentProcess()), processPowerThrottling, uintptr(unsafe.Pointer(&policy)), unsafe.Sizeof(policy)); result == 0 {
		t.Fatalf("read this process's power policy: %v", err)
	}
	return policy
}

// The engine's worker runs hidden, as a child of a board that is often
// started at login with no window, and Windows throttles such a process onto
// slow scheduling: a loaded engine answered in 4 to 5 s instead of 0.2 s. So
// the worker asks for the scheduling cfo serve asks for, before anything else.
func TestTheVoiceWorkerAsksNotToBeThrottled(t *testing.T) {
	// Arrange
	left := powerPolicy{Version: 1}
	if result, _, err := setProcessInformation.Call(uintptr(windows.CurrentProcess()), processPowerThrottling, uintptr(unsafe.Pointer(&left)), unsafe.Sizeof(left)); result == 0 {
		t.Fatalf("leave this process's power policy to Windows: %v", err)
	}
	if policy := currentPowerPolicy(t); policy.ControlMask&executionSpeed != 0 {
		t.Fatalf("the policy was not left to Windows: %+v", policy)
	}

	// Act
	run([]string{"voice-worker", "engine.dll"}, &bytes.Buffer{}, &bytes.Buffer{})

	// Assert
	if policy := currentPowerPolicy(t); policy.ControlMask&executionSpeed == 0 || policy.StateMask&executionSpeed != 0 {
		t.Fatalf("the voice worker's power policy is %+v, want execution speed controlled and not throttled", policy)
	}
}
