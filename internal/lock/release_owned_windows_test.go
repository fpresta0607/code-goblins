package lock

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestOwnedReleaseDeadlineCoversUnreadableRecord(t *testing.T) {
	dir, name := t.TempDir(), "controlled-lease"
	owner, err := AcquireExclusiveNamedStrictContext(context.Background(), dir, name)
	if err != nil {
		t.Fatal(err)
	}
	path, err := windows.UTF16PtrFromString(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(path, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	err = ReleaseExclusiveOwned(ctx, dir, name, *owner)
	elapsed := time.Since(started)
	windows.CloseHandle(handle)
	if !errors.Is(err, context.DeadlineExceeded) || elapsed > time.Second {
		t.Errorf("unreadable record ignored deadline: %v after %s", err, elapsed)
	}
	if err := ReleaseExclusiveOwned(context.Background(), dir, name, *owner); err != nil {
		t.Fatal(err)
	}
}
