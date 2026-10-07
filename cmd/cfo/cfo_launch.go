package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/fpresta0607/code-goblins/internal/lock"
)

func acquireCFOLaunch(ctx context.Context, stateDir string) error {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		_, err := lock.AcquireExclusiveNamed(stateDir, cfoLaunchLock)
		if err == nil || !errors.Is(err, lock.ErrHeld) {
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("another start of the CFO is still under way: %w", errors.Join(err, ctx.Err()))
		case <-time.After(100 * time.Millisecond):
		}
	}
}
