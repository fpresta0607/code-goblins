package proc

import (
	"fmt"
	"sync"
	"syscall"

	"golang.org/x/sys/windows"
)

// Windows keeps a callback for as long as the program runs and has room for
// a fixed number of them, so every listing shares one, which fills found
// while windowListing is held.
var (
	windowListing  sync.Mutex
	windowCallback = sync.OnceValue(func() uintptr {
		return syscall.NewCallback(func(window windows.HWND, _ uintptr) uintptr {
			if windows.IsWindowVisible(window) {
				var pid uint32
				if _, err := windows.GetWindowThreadProcessId(window, &pid); err == nil && pid != 0 {
					found = append(found, shownWindow{window: window, owner: int(pid)})
				}
			}
			return 1
		})
	})
	found []shownWindow
)

// shownWindow is one visible top-level window and the process that owns it.
type shownWindow struct {
	window windows.HWND
	owner  int
}

// shownWindows lists the visible top-level windows on this process's
// desktop.
func shownWindows() ([]shownWindow, error) {
	windowListing.Lock()
	defer windowListing.Unlock()
	found = nil
	if err := windows.EnumWindows(windowCallback(), nil); err != nil {
		return nil, fmt.Errorf("proc: list the desktop's windows: %w", err)
	}
	shown := found
	found = nil
	return shown, nil
}

// WindowOwners are the processes that show a window on this process's
// desktop, by ID: each owns a visible top-level window. A program a person
// can see is one they use and can close themselves, which no command line or
// parent says of it.
func WindowOwners() (map[int]bool, error) {
	shown, err := shownWindows()
	if err != nil {
		return nil, err
	}
	owners := map[int]bool{}
	for _, each := range shown {
		owners[each.owner] = true
	}
	return owners, nil
}

// CloseWindows asks each window process pid shows to close, as its close
// button would, and says how many it asked. It closes no window of any other
// process and ends no process, so a program that runs on without its
// window, as Docker Desktop does in the tray, runs on.
func CloseWindows(pid int) (int, error) {
	shown, err := shownWindows()
	if err != nil {
		return 0, err
	}
	asked := 0
	for _, each := range shown {
		if each.owner != pid {
			continue
		}
		isAsked, err := closeWindow(each.window, pid)
		if err != nil {
			return asked, err
		}
		if isAsked {
			asked++
		}
	}
	return asked, nil
}

// postMessage is user32's PostMessageW, which x/sys/windows does not wrap.
var postMessage = windows.NewLazySystemDLL("user32.dll").NewProc("PostMessageW")

// windowClose is WM_CLOSE, what a window's close button sends it.
const windowClose = 0x0010

// closeWindow asks window to close, as its close button would, while pid
// owns it, and says whether it asked. The owner is read again here: a window
// that closed since it was listed leaves its handle free for the next window
// made, which may be another program's.
func closeWindow(window windows.HWND, pid int) (bool, error) {
	var owner uint32
	if _, err := windows.GetWindowThreadProcessId(window, &owner); err != nil || int(owner) != pid {
		return false, nil
	}
	if posted, _, err := postMessage.Call(uintptr(window), windowClose, 0, 0); posted == 0 {
		return false, fmt.Errorf("proc: ask window %#x of pid %d to close: %w", window, pid, err)
	}
	return true, nil
}
