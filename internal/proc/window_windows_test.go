package proc

import (
	"os"
	"runtime"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32            = windows.NewLazySystemDLL("user32.dll")
	procCreateWindow  = user32.NewProc("CreateWindowExW")
	procDestroyWindow = user32.NewProc("DestroyWindow")
)

// A process is a window's owner only while the window shows. The window here
// is one pixel placed far off the screen, with no taskbar button and never
// activated, so the test shows nothing and takes no focus.
func TestWindowOwnersAreTheProcessesShowingAWindow(t *testing.T) {
	// Arrange
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	before, err := WindowOwners()
	if err != nil {
		t.Fatal(err)
	}
	if before[os.Getpid()] {
		t.Fatal("this test shows a window before it made one")
	}
	const (
		toolWindow = 0x00000080
		noActivate = 0x08000000
		popup      = 0x80000000
		visible    = 0x10000000
	)
	// Far off every screen, as a signed coordinate.
	offScreen := int32(-32000)
	class, err := windows.UTF16PtrFromString("STATIC")
	if err != nil {
		t.Fatal(err)
	}
	position := uintptr(uint32(offScreen))
	window, _, createErr := procCreateWindow.Call(toolWindow|noActivate, uintptr(unsafe.Pointer(class)), 0, popup|visible, position, position, 1, 1, 0, 0, 0, 0)
	if window == 0 {
		t.Skipf("this session has no desktop to make a window on: %v", createErr)
	}

	// Act
	shown, shownErr := WindowOwners()
	if ok, _, err := procDestroyWindow.Call(window); ok == 0 {
		t.Fatal(err)
	}
	after, afterErr := WindowOwners()

	// Assert
	if shownErr != nil || !shown[os.Getpid()] {
		t.Errorf("with a window showing, this process is an owner = %t, %v; want true", shown[os.Getpid()], shownErr)
	}
	if afterErr != nil || after[os.Getpid()] {
		t.Errorf("with its window gone, this process is an owner = %t, %v; want false", after[os.Getpid()], afterErr)
	}
}
