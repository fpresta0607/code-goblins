package fsx

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
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

// The wait is bounded: a reader that never lets go costs a write
// transientBudget, then the write fails and leaves no temp file behind.
func TestAtomicWriteFileGivesUpOnAReaderThatNeverLetsGo(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	path := filepath.Join(dir, ".supervisor.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	reader, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	budget := transientBudget
	transientBudget = 300 * time.Millisecond
	t.Cleanup(func() { transientBudget = budget })

	// Act
	started := time.Now()
	err = AtomicWriteFile(path, []byte("new"))
	took := time.Since(started)

	// Assert
	if err == nil {
		t.Fatal("AtomicWriteFile replaced a file a reader never let go of")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("the error %q does not name %s", err, path)
	}
	if took > 2*time.Second {
		t.Errorf("AtomicWriteFile took %s to give up, want about %s", took, transientBudget)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("the directory holds %d entries, want only the file (no temp file left)", len(entries))
	}
}

// A removal waits out another process holding the file, as a replace does.
func TestRemoveWaitsOutAReaderThatHoldsTheFile(t *testing.T) {
	// Arrange
	path := filepath.Join(t.TempDir(), ".supervise-notified")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
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
	err = Remove(path)
	<-released

	// Assert
	if err != nil {
		t.Fatalf("Remove while a reader held the file for 1.5 s: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the file is still there after Remove: %v", err)
	}
}
