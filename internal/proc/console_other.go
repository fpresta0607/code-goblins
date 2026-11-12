//go:build !windows

package proc

// ConsoleShown refuses: a console window is a Windows thing.
func ConsoleShown(pid int, windowOwners map[int]bool) (bool, error) {
	return false, notPorted("finding a process's console window")
}

// ConsoleHost refuses: a console host is a Windows process.
func ConsoleHost(pid int) (int, error) {
	return 0, notPorted("finding a process's console host")
}
