//go:build !windows

package fsx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
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

// A removal that waits out another's claim waits with the file that had the
// name when it began, and the other may have put another file there since.
// It checks the file that has the name once the claim is its own, never the
// one it waited with, which is no longer anybody's record.
func TestCheckedRemovalChecksTheFileThatTookTheNameWhileItsClaimWasWaitedFor(t *testing.T) {
	// Arrange
	path := filepath.Join(t.TempDir(), "record")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	type removal struct {
		checked   []string
		isRemoved bool
		err       error
	}
	contended := make(chan removal, 1)

	// Act
	_, err := RemoveFileWhen(context.Background(), path, func([]byte) (bool, error) {
		opened := Opens()
		go func() {
			var contender removal
			contender.isRemoved, contender.err = RemoveFileWhen(context.Background(), path, func(data []byte) (bool, error) {
				contender.checked = append(contender.checked, string(data))
				return true, nil
			})
			contended <- contender
		}()
		// The contender has the old file open once its open is counted, and
		// waits for the claim this check is made under.
		for deadline := time.Now().Add(30 * time.Second); Opens() == opened; time.Sleep(time.Millisecond) {
			if time.Now().After(deadline) {
				return false, errors.New("the contender did not open the record within 30 s")
			}
		}
		if err := os.Remove(path); err != nil {
			return false, err
		}
		return false, os.WriteFile(path, []byte("replacement"), 0o600)
	})
	contender := <-contended

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(contender.checked, []string{"replacement"}) {
		t.Errorf("the contender checked %q, want only the file that took the name", contender.checked)
	}
	if contender.err != nil || !contender.isRemoved {
		t.Errorf("the contender's removal = %t, %v, want the file its check approved removed", contender.isRemoved, contender.err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the file the contender's check approved is still there: %v", err)
	}
}
