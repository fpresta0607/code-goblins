package proc

import (
	"fmt"
	"os"
	"runtime"
	"slices"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32            = windows.NewLazySystemDLL("user32.dll")
	procCreateWindow  = user32.NewProc("CreateWindowExW")
	procDestroyWindow = user32.NewProc("DestroyWindow")
	procPeekMessage   = user32.NewProc("PeekMessageW")
	procDispatch      = user32.NewProc("DispatchMessageW")
)

// hiddenWindow makes a real top-level window that never shows: it is made
// without WS_VISIBLE, and it is one pixel far off the screen with no taskbar
// button and never activated besides, so no person sees it and the start
// watch, which reads what shows, counts nothing. The caller holds its
// thread, which owns the window and receives its messages.
func hiddenWindow(t *testing.T) windows.HWND {
	t.Helper()
	const (
		toolWindow = 0x00000080
		noActivate = 0x08000000
		popup      = 0x80000000
	)
	// Far off every screen, as a signed coordinate.
	offScreen := int32(-32000)
	class, err := windows.UTF16PtrFromString("STATIC")
	if err != nil {
		t.Fatal(err)
	}
	title, err := windows.UTF16PtrFromString(hiddenWindowTitle)
	if err != nil {
		t.Fatal(err)
	}
	position := uintptr(uint32(offScreen))
	window, _, createErr := procCreateWindow.Call(toolWindow|noActivate, uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(title)), popup, position, position, 1, 1, 0, 0, 0, 0)
	if window == 0 {
		t.Skipf("this session has no desktop to make a window on: %v", createErr)
	}
	if windows.IsWindowVisible(windows.HWND(window)) {
		procDestroyWindow.Call(window)
		t.Fatal("the test's window shows, and a test shows nothing")
	}
	return windows.HWND(window)
}

// A process is a window's owner only while a window of its shows: one that
// does not show, as a tray program's do, makes it none.
func TestWindowOwnersLeaveOutAWindowThatDoesNotShow(t *testing.T) {
	// Arrange
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	window := hiddenWindow(t)
	defer procDestroyWindow.Call(uintptr(window))

	// Act
	owners, err := WindowOwners()

	// Assert
	if err != nil || owners[os.Getpid()] {
		t.Errorf("with only a window that does not show, this process is an owner = %t, %v; want false", owners[os.Getpid()], err)
	}
}

// hiddenWindowTitle is the title the tests' window is made with.
const hiddenWindowTitle = "cg proc test window"

// Windows lists a process's windows that do not show as well as those that
// do, each with its class and title, which is how a window is told apart
// before it shows.
func TestWindowsListsAWindowThatDoesNotShowWithItsClassAndTitle(t *testing.T) {
	// Arrange
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	window := hiddenWindow(t)
	defer procDestroyWindow.Call(uintptr(window))

	// Act
	owned, err := Windows(os.Getpid())
	others, othersErr := Windows(os.Getpid() + 4)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	want := Window{Handle: window, Owner: os.Getpid(), IsShown: false, Class: "Static", Title: hiddenWindowTitle}
	if !slices.Contains(owned, want) {
		t.Errorf("windows = %+v, want %+v among them", owned, want)
	}
	if othersErr != nil || slices.ContainsFunc(others, func(each Window) bool { return each.Handle == window }) {
		t.Errorf("another process's windows = %+v, err %v, want this window not among them", others, othersErr)
	}
}

// A window is asked to close by its handle only while the process it was
// listed with owns it: a handle can pass to another program's window once
// its own closed, and that window is left alone. The window closes as its
// close button closes it, once its thread reads the message.
func TestCloseWindowAsksAWindowOnlyWhileItsOwnerOwnsIt(t *testing.T) {
	// Arrange
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	window := hiddenWindow(t)
	defer procDestroyWindow.Call(uintptr(window))
	// Process ids are multiples of four, and this one is not this process.
	other := os.Getpid() + 4

	// Act
	askedOther, otherErr := CloseWindow(Window{Handle: window, Owner: other})
	pump()
	isOpenAfterOther := windows.IsWindow(window)
	asked, err := CloseWindow(Window{Handle: window, Owner: os.Getpid()})
	for deadline := time.Now().Add(time.Minute); windows.IsWindow(window) && time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		pump()
	}

	// Assert
	if otherErr != nil || askedOther || !isOpenAfterOther {
		t.Errorf("naming another process asked = %t, err %v, and left the window open = %t; want false, nil, true", askedOther, otherErr, isOpenAfterOther)
	}
	if err != nil || !asked {
		t.Errorf("naming its owner asked = %t, err %v; want true, nil", asked, err)
	}
	if windows.IsWindow(window) {
		t.Error("the window is still open after its owner's close was asked")
	}
}

// pump hands every message waiting on this thread to its window.
func pump() {
	// The layout of Windows' MSG.
	var message struct {
		window         uintptr
		message        uint32
		wParam, lParam uintptr
		time           uint32
		x, y           int32
		private        uint32
	}
	const remove = 1
	for {
		if waiting, _, _ := procPeekMessage.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0, remove); waiting == 0 {
			return
		}
		procDispatch.Call(uintptr(unsafe.Pointer(&message)))
	}
}

var (
	procCreateDesktop    = user32.NewProc("CreateDesktopW")
	procCloseDesktop     = user32.NewProc("CloseDesktop")
	procGetThreadDesktop = user32.NewProc("GetThreadDesktop")
	procSetThreadDesktop = user32.NewProc("SetThreadDesktop")
)

// onEmptyDesktop runs act on a thread of its own placed on a desktop made
// for it, which holds no window until act makes one, as the hidden desktop
// a gate's test step starts its agents on held none (#1799).
func onEmptyDesktop(t *testing.T, act func()) {
	t.Helper()
	const desktopAll = 0x01ff
	name, err := windows.UTF16PtrFromString(fmt.Sprintf("cg-proc-test-%d-%d", os.Getpid(), time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	desktop, _, createErr := procCreateDesktop.Call(uintptr(unsafe.Pointer(name)), 0, 0, 0, desktopAll, 0)
	if desktop == 0 {
		t.Skipf("this session cannot make a desktop: %v", createErr)
	}
	defer procCloseDesktop.Call(desktop)
	placed := make(chan error, 1)
	done := make(chan struct{})
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
		act()
	}()
	if err := <-placed; err != nil {
		t.Fatalf("the test's thread could not be placed on its desktop: %v", err)
	}
	<-done
}

// A desktop that holds no window, as the hidden desktop a gate's test step
// starts its agents on held none, has no window owners and lists no window
// of this process: an empty listing, not a failure (#1799). A window made
// there is still listed, so the listing reads that desktop.
func TestWindowsListNothingOnADesktopWithNoWindows(t *testing.T) {
	// Arrange
	class, err := windows.UTF16PtrFromString("STATIC")
	if err != nil {
		t.Fatal(err)
	}
	title, err := windows.UTF16PtrFromString(hiddenWindowTitle)
	if err != nil {
		t.Fatal(err)
	}
	var (
		owners          map[int]bool
		ownersErr       error
		listed, withOne []Window
		listedErr       error
		withOneErr      error
		createErr       error
	)
	onEmptyDesktop(t, func() {
		// Act
		owners, ownersErr = WindowOwners()
		listed, listedErr = Windows(os.Getpid())
		// A window made there, never shown, as hiddenWindow makes one.
		window, _, err := procCreateWindow.Call(0, uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(title)), 0x80000000, 0, 0, 1, 1, 0, 0, 0, 0)
		if window == 0 {
			createErr = err
			return
		}
		defer procDestroyWindow.Call(window)
		withOne, withOneErr = Windows(os.Getpid())
	})

	// Assert
	if ownersErr != nil || len(owners) != 0 {
		t.Errorf("WindowOwners() on an empty desktop = %v, %v; want no owners and no error", owners, ownersErr)
	}
	if listedErr != nil || len(listed) != 0 {
		t.Errorf("Windows(this process) on an empty desktop = %+v, %v; want none and no error", listed, listedErr)
	}
	if createErr != nil {
		t.Fatalf("no window could be made on the test's desktop: %v", createErr)
	}
	if withOneErr != nil || !slices.ContainsFunc(withOne, func(window Window) bool { return window.Title == hiddenWindowTitle }) {
		t.Errorf("Windows(this process) with a window made there = %+v, %v; want that window among them", withOne, withOneErr)
	}
}
