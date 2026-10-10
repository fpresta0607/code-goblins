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
					found[int(pid)] = true
				}
			}
			return 1
		})
	})
	found map[int]bool
)

// WindowOwners are the processes that show a window on this process's
// desktop, by ID: each owns a visible top-level window. A program a person
// can see is one they use and can close themselves, which no command line or
// parent says of it.
func WindowOwners() (map[int]bool, error) {
	windowListing.Lock()
	defer windowListing.Unlock()
	found = map[int]bool{}
	if err := windows.EnumWindows(windowCallback(), nil); err != nil {
		return nil, fmt.Errorf("proc: list the desktop's windows: %w", err)
	}
	owners := found
	found = nil
	return owners, nil
}
