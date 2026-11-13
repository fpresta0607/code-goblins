package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32               = windows.NewLazySystemDLL("user32.dll")
	procCreateDesktop    = user32.NewProc("CreateDesktopW")
	procCloseDesktop     = user32.NewProc("CloseDesktop")
	procGetThreadDesktop = user32.NewProc("GetThreadDesktop")
	procSetThreadDesktop = user32.NewProc("SetThreadDesktop")
)

// A process read from a thread on a desktop that holds no window, as the
// hidden desktop a gate's test step starts its agents on held none, counts
// as showing a window: that listing cannot show any program to be none of
// the Overlord's (#1799).
func TestProcessesReadOnADesktopWithNoWindowsAllShowAWindow(t *testing.T) {
	// Arrange
	const desktopAll = 0x01ff
	name, err := windows.UTF16PtrFromString(fmt.Sprintf("cg-lifecycle-test-%d-%d", os.Getpid(), time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	desktop, _, createErr := procCreateDesktop.Call(uintptr(unsafe.Pointer(name)), 0, 0, 0, desktopAll, 0)
	if desktop == 0 {
		t.Skipf("this session cannot make a desktop: %v", createErr)
	}
	defer procCloseDesktop.Call(desktop)
	var (
		processes []Process
		readErr   error
	)
	placed := make(chan error, 1)
	done := make(chan struct{})

	// Act
	go func() {
		defer close(done)
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		own, _, _ := procGetThreadDesktop.Call(uintptr(windows.GetCurrentThreadId()))
		if ok, _, setErr := procSetThreadDesktop.Call(desktop); ok == 0 {
			placed <- setErr
			return
		}
		defer procSetThreadDesktop.Call(own)
		placed <- nil
		processes, readErr = ReadAllProcesses(context.Background(), false)
	}()
	if err := <-placed; err != nil {
		t.Fatalf("the test's thread could not be placed on its desktop: %v", err)
	}
	<-done

	// Assert
	if readErr != nil {
		t.Fatalf("ReadAllProcesses: %v", readErr)
	}
	foundSelf := false
	for _, process := range processes {
		if process.PID == 0 {
			continue
		}
		if !process.HasWindow {
			t.Errorf("process %d (%s) shows no window, want every process to show one", process.PID, process.Name)
		}
		foundSelf = foundSelf || process.PID == os.Getpid()
	}
	if !foundSelf {
		t.Fatalf("this process %d was not read", os.Getpid())
	}
}

func TestAnUnreadableOrEmptyWindowListingShowsEveryProcessAWindow(t *testing.T) {
	testCases := []struct {
		name         string
		windowOwners map[int]bool
		windowsErr   error
		pid          int
		want         bool
	}{
		{name: "an empty listing protects any process", windowOwners: map[int]bool{}, pid: 4242, want: true},
		{name: "no listing protects any process", windowOwners: nil, pid: 4242, want: true},
		{name: "a failed listing protects any process", windowsErr: errors.New("proc: list the desktop's windows: access denied"), pid: 4242, want: true},
		{name: "a listing shows its owner a window", windowOwners: map[int]bool{100: true}, pid: 100, want: true},
		{name: "a listing shows no window to a process it leaves out", windowOwners: map[int]bool{100: true}, pid: 4242, want: false},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := showsWindow(testCase.windowOwners, testCase.windowsErr, testCase.pid)

			if got != testCase.want {
				t.Fatalf("showsWindow(%v, %v, %d) = %t, want %t", testCase.windowOwners, testCase.windowsErr, testCase.pid, got, testCase.want)
			}
		})
	}
}
