package fsx

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// An operation run through WaitOut gets the wait a replace has: a rename of
// a file a reader holds, as a virus scanner holds one it just saw, is refused
// and tried again, and goes through once the reader has let go. The reader
// here lets go only after the first try was refused, so the test waits on no
// clock.
func TestWaitOutTriesAgainOnceAReaderLetsGo(t *testing.T) {
	// Arrange
	path := filepath.Join(t.TempDir(), "cfo.exe")
	if err := os.WriteFile(path, []byte("a build"), 0o700); err != nil {
		t.Fatal(err)
	}
	reader, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var refused error
	tries := 0

	// Act
	err = WaitOut(func() error {
		tries++
		err := os.Rename(path, path+".old")
		if tries == 1 {
			refused = err
			reader.Close()
		}
		return err
	})

	// Assert
	if refused == nil {
		t.Fatal("the rename went through while the reader held the file, so this run proved nothing")
	}
	if err != nil || tries != 2 {
		t.Fatalf("WaitOut of a rename a reader refused once = %v after %d tries, want it done on the second", err, tries)
	}
	if _, err := os.Stat(path + ".old"); err != nil {
		t.Errorf("the file was not renamed: %v", err)
	}
}

// What is refused for any other reason is not tried again: the error comes
// back from the first try.
func TestWaitOutDoesNotRetryWhatNoHolderRefused(t *testing.T) {
	// Arrange
	missing := filepath.Join(t.TempDir(), "never-made")
	tries := 0

	// Act
	err := WaitOut(func() error {
		tries++
		return os.Rename(missing, missing+".old")
	})

	// Assert
	if !errors.Is(err, os.ErrNotExist) || tries != 1 {
		t.Errorf("WaitOut of a rename of a missing file = %v after %d tries, want its not-exist error after 1", err, tries)
	}
}
