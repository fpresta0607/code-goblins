package fsx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
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
