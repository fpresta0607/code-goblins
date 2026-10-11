package proc

import (
	"fmt"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Window is one top-level window on this process's desktop: its handle, the
// process that owns it, whether it shows, and, as Windows lists it, the class
// it was made with and its title.
type Window struct {
	Handle  windows.HWND
	Owner   int
	IsShown bool
	Class   string
	Title   string
}

// Windows keeps a callback for as long as the program runs and has room for
// a fixed number of them, so every listing shares one, which fills found
// while windowListing is held.
var (
	windowListing  sync.Mutex
	windowCallback = sync.OnceValue(func() uintptr {
		return syscall.NewCallback(func(window windows.HWND, _ uintptr) uintptr {
			var pid uint32
			if _, err := windows.GetWindowThreadProcessId(window, &pid); err == nil && pid != 0 {
				found = append(found, Window{Handle: window, Owner: int(pid), IsShown: windows.IsWindowVisible(window)})
			}
			return 1
		})
	})
	found []Window
)

// topLevelWindows lists the top-level windows on this process's desktop,
// shown or not, without their class or title.
func topLevelWindows() ([]Window, error) {
	windowListing.Lock()
	defer windowListing.Unlock()
	found = nil
	if err := windows.EnumWindows(windowCallback(), nil); err != nil {
		return nil, fmt.Errorf("proc: list the desktop's windows: %w", err)
	}
	listed := found
	found = nil
	return listed, nil
}

// WindowOwners are the processes that show a window on this process's
// desktop, by ID: each owns a visible top-level window. A program a person
// can see is one they use and can close themselves, which no command line or
// parent says of it.
func WindowOwners() (map[int]bool, error) {
	listed, err := topLevelWindows()
	if err != nil {
		return nil, err
	}
	owners := map[int]bool{}
	for _, each := range listed {
		if each.IsShown {
			owners[each.Owner] = true
		}
	}
	return owners, nil
}

// getWindowText is user32's GetWindowTextW, which x/sys/windows does not
// wrap. For another process's window it reads the title Windows keeps and
// sends that process nothing, so a busy program cannot hold it up.
var getWindowText = windows.NewLazySystemDLL("user32.dll").NewProc("GetWindowTextW")

// Windows lists the top-level windows process pid owns, shown or not, each
// with its class and title.
func Windows(pid int) ([]Window, error) {
	listed, err := topLevelWindows()
	if err != nil {
		return nil, err
	}
	var owned []Window
	for _, each := range listed {
		if each.Owner != pid {
			continue
		}
		text := make([]uint16, 256)
		if copied, err := windows.GetClassName(each.Handle, &text[0], int32(len(text))); err == nil {
			each.Class = windows.UTF16ToString(text[:copied])
		}
		copied, _, _ := getWindowText.Call(uintptr(each.Handle), uintptr(unsafe.Pointer(&text[0])), uintptr(len(text)))
		each.Title = windows.UTF16ToString(text[:copied])
		owned = append(owned, each)
	}
	return owned, nil
}

// postMessage is user32's PostMessageW, which x/sys/windows does not wrap.
var postMessage = windows.NewLazySystemDLL("user32.dll").NewProc("PostMessageW")

// windowClose is WM_CLOSE, what a window's close button sends it.
const windowClose = 0x0010

// CloseWindow asks window to close, as its close button would, while the
// process that owned it when it was listed owns it still, and says whether
// it asked. A window that closed since it was listed leaves its handle free
// for the next window made, which may be another program's. It ends no
// process, so a program that runs on without its window, as Docker Desktop
// does in the tray, runs on.
func CloseWindow(window Window) (bool, error) {
	var owner uint32
	if _, err := windows.GetWindowThreadProcessId(window.Handle, &owner); err != nil || int(owner) != window.Owner {
		return false, nil
	}
	if posted, _, err := postMessage.Call(uintptr(window.Handle), windowClose, 0, 0); posted == 0 {
		return false, fmt.Errorf("proc: ask window %#x of pid %d to close: %w", window.Handle, window.Owner, err)
	}
	return true, nil
}
