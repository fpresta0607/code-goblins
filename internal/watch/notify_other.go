//go:build !windows

package watch

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// DirWaiter would wake a wait when a folder changes, which this system does
// not watch yet: its callers wait on their timers instead.
type DirWaiter struct{}

// NewDirWaiter refuses: watching a folder for changes is not ported to this
// system yet.
func NewDirWaiter(dir string) (*DirWaiter, error) {
	return nil, fmt.Errorf("watch: watching %s for changes is not ported to this system yet: %w", dir, errors.ErrUnsupported)
}

// NewNativeWaiter refuses, as NewDirWaiter does.
func NewNativeWaiter(dir string) (*DirWaiter, error) {
	return NewDirWaiter(dir)
}

// Wait waits out timeout or ctx, since no change is ever seen.
func (w *DirWaiter) Wait(ctx context.Context, timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
	return false
}

// Degraded reports true: this waiter only ever waits on its timer.
func (w *DirWaiter) Degraded() bool { return true }

// Close does nothing, since NewDirWaiter opens nothing.
func (w *DirWaiter) Close() {}
