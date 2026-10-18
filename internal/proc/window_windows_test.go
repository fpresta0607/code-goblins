package proc

import (
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
