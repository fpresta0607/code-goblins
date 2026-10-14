package voice

import (
	"os"
	"syscall"
)

func killWorker(process *os.Process) error {
	return killWorkerWithHandle(process, process.Kill)
}

func killWorkerWithHandle(process *os.Process, kill func() error) error {
	var killErr error
	err := process.WithHandle(func(handle uintptr) {
		killErr = kill()
		if killErr != nil {
			// The borrowed handle still names this child after concurrent Wait releases it.
			status, waitErr := syscall.WaitForSingleObject(syscall.Handle(handle), 0)
			if waitErr == nil && status == syscall.WAIT_OBJECT_0 {
				killErr = nil
			}
		}
	})
	if err != nil {
		return kill()
	}
	return killErr
}
