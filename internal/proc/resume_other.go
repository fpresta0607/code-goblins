//go:build !windows

package proc

// Resume refuses: a process created suspended is a Windows thing.
func Resume(pid int) error {
	return notPorted("resuming a suspended process")
}
