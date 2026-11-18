//go:build linux || darwin

package pipeline

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// lockDaemon takes the lock a no-mistakes daemon takes on daemon.lock off
// Windows, an exclusive flock (v1.75.1), without waiting, so no daemon starts
// between the idle check and the config's replacement. Closing the file
// releases it, as the end of this process does.
func lockDaemon(path string) (func() error, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("%w: daemon lock unavailable: %v", ErrBusy, err)
		}
		return nil, fmt.Errorf("pipeline: lock %s: %w", path, err)
	}
	return f.Close, nil
}
