//go:build !windows

package lock

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// place is a waiter's place in a line, which this system has none of yet.
type place struct{}

// lineUp refuses: the line of waiters is not ported to this system yet.
func lineUp(path string, waiting context.Context) (place, error) {
	return place{}, fmt.Errorf("lock: the line of waiters at %s is not ported to this system yet: %w", path, errors.ErrUnsupported)
}

// leaveLine does nothing, since lineUp hands out no place.
func leaveLine(place) {}

// keepPlace does nothing, since lineUp hands out no place.
func keepPlace(key string, at place) {}

// leavePlace does nothing, since lineUp hands out no place.
func leavePlace(key string) {}

// Show refuses: the sign is a place in a line, which is not ported to this
// system yet.
func Show(path string, wait time.Duration) (hide func(), err error) {
	return nil, fmt.Errorf("lock: the sign at %s is not ported to this system yet: %w", path, errors.ErrUnsupported)
}

// Shown reports no sign: none can be shown on this system yet.
func Shown(path string) bool {
	return false
}
