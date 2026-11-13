package main

import (
	"bytes"
	"errors"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/home"
)

func TestServeDisablesAutomaticExecutionSpeedThrottlingBeforeOpeningItsHome(t *testing.T) {
	// Arrange
	kernel := windows.NewLazySystemDLL("kernel32.dll")
	get := kernel.NewProc("GetProcessInformation")
	set := kernel.NewProc("SetProcessInformation")
	policy := struct{ Version, Control, State uint32 }{Version: 1}
	if result, _, err := get.Call(uintptr(windows.CurrentProcess()), 4, uintptr(unsafe.Pointer(&policy)), unsafe.Sizeof(policy)); result == 0 {
		t.Fatal(err)
	}
	original := policy
	t.Cleanup(func() {
		if result, _, err := set.Call(uintptr(windows.CurrentProcess()), 4, uintptr(unsafe.Pointer(&original)), unsafe.Sizeof(original)); result == 0 {
			t.Error(err)
		}
	})
	policy.Control, policy.State = 0, 0
	if result, _, err := set.Call(uintptr(windows.CurrentProcess()), 4, uintptr(unsafe.Pointer(&policy)), unsafe.Sizeof(policy)); result == 0 {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	hasOpened := false
	runtime := commandRuntime{resolveHome: func() (home.Home, error) {
		hasOpened = true
		return home.Home{}, errors.New("stop before opening a board")
	}}

	// Act
	code := runServe([]string{"--listen", "127.0.0.1:0"}, &stdout, &stderr, runtime)

	// Assert
	if code != 1 || !hasOpened {
		t.Fatalf("serve never reached home resolution: exit %d, %s", code, stderr.String())
	}
	if result, _, err := get.Call(uintptr(windows.CurrentProcess()), 4, uintptr(unsafe.Pointer(&policy)), unsafe.Sizeof(policy)); result == 0 {
		t.Fatal(err)
	}
	if policy.Control&1 == 0 || policy.State&1 != 0 {
		t.Errorf("serve kept automatic/throttled scheduling: control=%#x state=%#x", policy.Control, policy.State)
	}
}

// The board's supervisor reads which process holds the most handles from this
// machine, so its memory reading can name it and a leak past the handle line
// can wake the CEO.
func TestServeDispatchReadsTheTopHandleHolderOfThisMachine(t *testing.T) {
	// Arrange
	dispatch := serveDispatch(testHome(t))
	if dispatch.TopHandleHolder == nil {
		t.Fatal("serve's Dispatch reads no handle holder")
	}

	// Act
	holder, err := dispatch.TopHandleHolder()

	// Assert
	if err != nil {
		t.Fatalf("serve's TopHandleHolder: %v", err)
	}
	if holder.PID <= 0 || holder.Name == "" || holder.Handles == 0 || holder.Started.IsZero() {
		t.Fatalf("serve's TopHandleHolder = %+v, want a running process of this machine with the handles it holds", holder)
	}
}
