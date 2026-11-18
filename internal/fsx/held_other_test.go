//go:build !windows

package fsx

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// A reader that holds a file keeps a replace from landing until it lets go,
// as a Windows reader that does not share deletion does, so what it read is
// what the name still holds while it acts on it.
func TestCommitUnheldWaitsForTheReaderThatHoldsTheFile(t *testing.T) {
	// Arrange
	budget := transientBudget
	transientBudget = 2 * time.Minute
	waiting := make(chan struct{})
	var once sync.Once
	sleep = func(wait time.Duration) {
		once.Do(func() { close(waiting) })
		time.Sleep(wait)
	}
	t.Cleanup(func() { transientBudget, sleep = budget, time.Sleep })
	path := filepath.Join(t.TempDir(), "primary.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	reader, err := OpenHeld(path)
	if err != nil {
		t.Fatal(err)
	}
	staged, err := Stage(path, []byte("new"))
	if err != nil {
		t.Fatal(err)
	}

	// Act
	committed := make(chan error, 1)
	go func() { committed <- staged.CommitUnheld() }()
	// The commit pauses between tries only once it has found the file held.
	select {
	case err := <-committed:
		t.Fatalf("CommitUnheld = %v while a reader held the file, want it to wait", err)
	case <-waiting:
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "old" {
		t.Fatalf("the name holds %q, %v while the reader holds it, want old", data, err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}

	// Assert
	if err := <-committed; err != nil {
		t.Fatalf("CommitUnheld = %v once the reader let go, want nil", err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "new" {
		t.Fatalf("the name holds %q, %v, want new", data, err)
	}
}

// A reader that holds the file past the budget fails the replace, which
// leaves the file and nothing staged beside it.
func TestCommitUnheldGivesUpOnAReaderThatOutlastsTheBudget(t *testing.T) {
	// Arrange
	budget := transientBudget
	transientBudget = 300 * time.Millisecond
	t.Cleanup(func() { transientBudget = budget })
	folder := t.TempDir()
	path := filepath.Join(folder, "primary.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	reader, err := OpenHeld(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	staged, err := Stage(path, []byte("new"))
	if err != nil {
		t.Fatal(err)
	}

	// Act
	err = staged.CommitUnheld()

	// Assert
	if err == nil {
		t.Fatal("CommitUnheld = nil while a reader held the file past the budget, want an error")
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "old" {
		t.Fatalf("the name holds %q, %v, want old", data, err)
	}
	entries, err := os.ReadDir(folder)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("the folder holds %d entries, want only primary.json", len(entries))
	}
}

// Readers share a file: one reader's hold never keeps another out.
func TestOpenHeldSharesTheFileWithAnotherReader(t *testing.T) {
	// Arrange
	path := filepath.Join(t.TempDir(), "primary.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := OpenHeld(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()

	// Act
	second, err := OpenHeld(path)

	// Assert
	if err != nil {
		t.Fatalf("OpenHeld = %v while another reader held the file, want it shared", err)
	}
	second.Close()
}

// A replace with no file at the name yet puts the new one there.
func TestCommitUnheldPutsAFileWhereNoneWas(t *testing.T) {
	// Arrange
	path := filepath.Join(t.TempDir(), "primary.json")
	staged, err := Stage(path, []byte("new"))
	if err != nil {
		t.Fatal(err)
	}

	// Act
	err = staged.CommitUnheld()

	// Assert
	if err != nil {
		t.Fatalf("CommitUnheld = %v, want nil", err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "new" {
		t.Fatalf("the name holds %q, %v, want new", data, err)
	}
}
