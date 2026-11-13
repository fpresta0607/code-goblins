//go:build !windows

package proc

import (
	"errors"
	"fmt"
	"time"
)

// notPorted is the error of a reading this package does only on Windows so
// far.
func notPorted(what string) error {
	return fmt.Errorf("proc: %s is not ported to this system yet: %w", what, errors.ErrUnsupported)
}

// CPUTime reports pid as not measured: reading a process's processor time is
// not ported yet.
func CPUTime(pid int) (time.Duration, bool) {
	return 0, false
}
