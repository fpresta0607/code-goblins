package gatehome

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// lockFile takes the lock on the file at path, and is false when another
// holds it. The system holds it on the open file, so it frees when the file
// closes, a crashed holder's included.
func lockFile(path string) (func() error, bool, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, false, err
	}
	err = windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, new(windows.Overlapped))
	if err == nil {
		return file.Close, true, nil
	}
	file.Close()
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return nil, false, nil
	}
	return nil, false, err
}
