package fsx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCheckedRemovalPinsTheValidatedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "record")
	if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	isRemoved, err := RemoveFileWhen(context.Background(), path, func(data []byte) (bool, error) {
		if string(data) != "old" {
			t.Fatalf("unexpected contents %q", data)
		}
		if err := os.Remove(path); !heldByAnother(err) {
			t.Fatalf("pathname could be removed during claim: %v", err)
		}
		if err := os.WriteFile(path, []byte("replacement"), 0600); !heldByAnother(err) {
			t.Fatalf("claimed contents could change: %v", err)
		}
		return true, nil
	})
	if err != nil || !isRemoved {
		t.Fatalf("removal: %t %v", isRemoved, err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("record remains: %v", err)
	}
}

func TestCheckedRemovalPreservesDeclinedOrCancelledRecords(t *testing.T) {
	for _, mode := range []string{"decline", "error", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "record")
			if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			checkError := errors.New("invalid identity")
			isRemoved, err := RemoveFileWhen(ctx, path, func([]byte) (bool, error) {
				switch mode {
				case "error":
					return false, checkError
				case "cancel":
					cancel()
					return true, nil
				default:
					return false, nil
				}
			})
			if isRemoved || (mode == "error" && !errors.Is(err, checkError)) || (mode == "cancel" && !errors.Is(err, context.Canceled)) || (mode == "decline" && err != nil) {
				t.Fatalf("removal: %t %v", isRemoved, err)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != "old" {
				t.Fatalf("record changed: %q %v", data, err)
			}
		})
	}
}

func TestCheckedRemovalBoundsClaimContention(t *testing.T) {
	path := filepath.Join(t.TempDir(), "record")
	if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	budget := transientBudget
	transientBudget = 2 * time.Minute
	t.Cleanup(func() { transientBudget = budget })
	_, err := RemoveFileWhen(context.Background(), path, func([]byte) (bool, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond) // stall-accepted: the test means this context to run out.
		defer cancel()
		started := time.Now()
		isRemoved, err := RemoveFileWhen(ctx, path, func([]byte) (bool, error) { t.Error("contender read a claimed record"); return true, nil })
		if isRemoved || !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > time.Minute {
			t.Fatalf("contender: %t %v", isRemoved, err)
		}
		return false, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if isRemoved, err := RemoveFileWhen(context.Background(), path, func([]byte) (bool, error) { return true, nil }); err != nil || !isRemoved {
		t.Fatalf("retry: %t %v", isRemoved, err)
	}
	if isRemoved, err := RemoveFileWhen(context.Background(), path, func([]byte) (bool, error) { t.Error("missing record checked"); return true, nil }); err != nil || isRemoved {
		t.Fatalf("missing: %t %v", isRemoved, err)
	}
}
