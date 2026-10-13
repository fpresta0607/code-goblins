package lock

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/fsx"
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

func TestContextAcquisitionBoundsAnUnfinishedPublisher(t *testing.T) {
	dir, name := t.TempDir(), "lease"
	writer, err := fsx.CreateNewContext(context.Background(), filepath.Join(dir, name), 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	owner, err := AcquireExclusiveNamedStrictContext(ctx, dir, name)
	if owner != nil || !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > time.Second {
		t.Fatalf("publisher wait ignored deadline: %+v %v", owner, err)
	}
	if _, err := writer.Write([]byte("interrupted")); err != nil {
		t.Fatalf("publisher was changed: %v", err)
	}
}

func TestContextAcquisitionPreservesInterruptedPublication(t *testing.T) {
	for _, contents := range []string{"", "{", "{}"} {
		t.Run(contents, func(t *testing.T) {
			dir, name := t.TempDir(), "lease"
			path := filepath.Join(dir, name)
			writer, err := fsx.CreateNewContext(context.Background(), path, 0600)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := writer.Write([]byte(contents)); err != nil {
				writer.Close()
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			owner, err := AcquireExclusiveNamedStrictContext(context.Background(), dir, name)
			if err == nil || owner != nil {
				t.Fatalf("interrupted record reclaimed: %+v %v", owner, err)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != contents {
				t.Fatalf("record changed: %q %v", data, err)
			}
		})
	}
}
