//go:build !windows

package proc

// Window is one top-level window on this process's desktop, which this system
// lists none of yet.
type Window struct {
	Handle  uintptr
	Owner   int
	IsShown bool
	Class   string
	Title   string
}

// WindowOwners refuses: listing the desktop's windows is not ported yet.
func WindowOwners() (map[int]bool, error) {
	return nil, notPorted("listing the desktop's windows")
}

// Windows refuses, as WindowOwners does.
func Windows(pid int) ([]Window, error) {
	return nil, notPorted("listing a process's windows")
}

// CloseWindow refuses, as WindowOwners does.
func CloseWindow(window Window) (bool, error) {
	return false, notPorted("closing a window")
}
