//go:build !windows

package gatehome

import (
	"errors"
	"os"
	"syscall"
)

// lockFile takes the kernel's lock on the file at path (flock), and is false
// when another holds it. It frees when the file closes, a crashed holder's
// included.
func lockFile(path string) (func() error, bool, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, false, err
	}
	err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		return file.Close, true, nil
	}
	file.Close()
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return nil, false, nil
	}
	return nil, false, err
}
