//go:build !windows

package fsx

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// An open file keeps nobody here from putting another file at its name. A
// removal whose check passed removes the file it checked and no other, so a
// file that took the name during the check stays.
func TestCheckedRemovalLeavesAFileThatTookTheNameDuringTheCheck(t *testing.T) {
	// Arrange
	path := filepath.Join(t.TempDir(), "record")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	isRemoved, err := RemoveFileWhen(context.Background(), path, func(data []byte) (bool, error) {
		if string(data) != "old" {
			t.Fatalf("the check read %q, want the file that was there", data)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		return true, os.WriteFile(path, []byte("replacement"), 0o600)
	})

	// Assert
	if err != nil || isRemoved {
		t.Fatalf("RemoveFileWhen = %t, %v, want nothing removed", isRemoved, err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "replacement" {
		t.Errorf("the file that took the name reads %q, %v, want it left as it was put", data, err)
	}
}
