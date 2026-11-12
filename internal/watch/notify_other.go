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

// Wait reports no change, since NewDirWaiter hands out no waiter to wait on.
func (w *DirWaiter) Wait(ctx context.Context, timeout time.Duration) bool {
	return false
}

// Close does nothing, since NewDirWaiter opens nothing.
func (w *DirWaiter) Close() {}
