package fsx

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A reader that opens a file the way Go's os.Open and PowerShell's
// Get-Content do, without FILE_SHARE_DELETE, makes a rename over that file
// fail with "Access is denied" for as long as it holds it. The Overlord's
// board showed exactly that on 2026-10-01 for state\.supervisor.json, held
// longer than the old half second of retries.
func TestAtomicWriteFileWaitsOutAReaderThatHoldsTheFile(t *testing.T) {
	// Arrange
	path := filepath.Join(t.TempDir(), ".supervisor.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	reader, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	go func() {
		time.Sleep(1500 * time.Millisecond)
		reader.Close()
		close(released)
	}()

	// Act
	err = AtomicWriteFile(path, []byte("new"))
	<-released

	// Assert
	if err != nil {
		t.Fatalf("AtomicWriteFile while a reader held the file for 1.5 s: %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "new" {
		t.Errorf("content = %q, want %q", got, "new")
	}
}

// A fleet reader opens with FILE_SHARE_DELETE, so a replace goes through
// while it reads, and the reader keeps reading the file it opened.
func TestAFleetReaderDoesNotBlockAReplace(t *testing.T) {
	// Arrange
	path := filepath.Join(t.TempDir(), ".supervisor.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	reader, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	// Act
	err = AtomicWriteFile(path, []byte("new"))

	// Assert
	if err != nil {
		t.Fatalf("AtomicWriteFile while a fleet reader held the file: %v", err)
	}
	if held, _ := io.ReadAll(reader); string(held) != "old" {
		t.Errorf("the open reader read %q, want the file it opened, %q", held, "old")
	}
	if got, err := ReadFile(path); err != nil || string(got) != "new" {
		t.Errorf("ReadFile after the replace = %q, %v, want %q", got, err, "new")
	}
}
