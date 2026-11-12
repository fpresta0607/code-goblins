//go:build !windows

package proc

// ConsoleWindowRole is the role a program with this package runs in to print
// a console's window, which only Windows has.
const ConsoleWindowRole = "ceo-console-window"

// ConsoleShown refuses: a console window is a Windows thing.
func ConsoleShown(pid int, windowOwners map[int]bool) (bool, error) {
	return false, notPorted("finding a process's console window")
}

// ConsoleHost refuses: a console host is a Windows process.
func ConsoleHost(pid int) (int, error) {
	return 0, notPorted("finding a process's console host")
}

// RunsAbove refuses: reading a process's priority is not ported yet.
func RunsAbove(pid int) (bool, error) {
	return false, notPorted("reading a process's priority")
}
