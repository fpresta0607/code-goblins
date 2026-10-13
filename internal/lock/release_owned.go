package lock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// ReleaseExclusiveOwned makes one removal attempt for precisely this lease.
// Failure preserves its in-process ownership for an explicit later retry.
func ReleaseExclusiveOwned(ctx context.Context, dir, name string, owner Info) error {
	if err := exclusiveBefore(ctx); err != nil {
		return err
	}
	defer exclusiveLeases.Unlock()
	key := exclusiveLeaseKey(dir, name)
	if acquired, held := exclusiveLeases.acquired[key]; !held || !acquired.Equal(owner.Acquired) || owner.PID != os.Getpid() || !owner.VerifiedAlive() {
		return fmt.Errorf("lock: no matching exclusive ownership for %s", name)
	}
	current, err := ReadNamedStrictContext(ctx, dir, name)
	if err == nil && *current != owner {
		return fmt.Errorf("lock: exclusive ownership changed for %s", name)
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	delete(exclusiveLeases.acquired, key)
	return nil
}

// AcquireExclusiveNamedStrictContext also bounds waiting for the in-process
// lease registry, which unrelated acquisitions and releases share.
func AcquireExclusiveNamedStrictContext(ctx context.Context, dir, name string) (*Info, error) {
	if err := exclusiveBefore(ctx); err != nil {
		return nil, err
	}
	defer exclusiveLeases.Unlock()
	key := exclusiveLeaseKey(dir, name)
	if _, held := exclusiveLeases.acquired[key]; held {
		return nil, fmt.Errorf("%w: exclusive task lock %s", ErrHeld, name)
	}
	self, _ := ownerInfo(os.Getpid(), exclusiveSpawnSession)
	if !self.VerifiedAlive() {
		return nil, fmt.Errorf("lock: cannot verify owner pid %d", self.PID)
	}
	data, err := json.Marshal(self)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, name)
	for attempt := 0; attempt < acquireAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		file, err := fsx.CreateNewContext(ctx, path, 0644)
		if err == nil {
			exclusiveLeases.acquired[key] = self.Acquired
			_, writeError := file.Write(data)
			return self, errors.Join(writeError, file.Close(), ctx.Err())
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		holder, err := ReadNamedStrictContext(ctx, dir, name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if holder.Alive() {
			return nil, heldError(holder)
		}
		current, err := ReadNamedStrictContext(ctx, dir, name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if *current != *holder {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("lock: strict acquisition changed during %d attempts", acquireAttempts)
}

func ReadNamedStrictContext(ctx context.Context, dir, name string) (*Info, error) {
	data, err := fsx.ReadFileContext(ctx, filepath.Join(dir, name))
	if err != nil {
		return nil, err
	}
	var info Info
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("lock: unreadable holder record: %w", err)
	}
	if info.PID <= 0 || info.OwnerPID != info.PID || info.Start.IsZero() || info.Acquired.IsZero() || info.Hostname == "" {
		return nil, fmt.Errorf("lock: incomplete custody in %s", filepath.Join(dir, name))
	}
	return &info, nil
}

func exclusiveBefore(ctx context.Context) error {
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for !exclusiveLeases.TryLock() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
	if err := ctx.Err(); err != nil {
		exclusiveLeases.Unlock()
		return err
	}
	return nil
}
