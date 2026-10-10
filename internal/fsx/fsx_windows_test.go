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

// The wait is bounded: a reader that never lets go costs a write at most
// transientBudget of waiting, then the write fails and leaves no temp file
// behind. The budget bounds the waits, not the file work around them, which
// on four busy processors took 0.6 to 0.85 s a step: the call took 2.97 s
// in all, 0.26 s of it waiting.
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
	var waited time.Duration
	sleep = func(wait time.Duration) {
		waited += wait
		time.Sleep(wait)
	}
	t.Cleanup(func() { transientBudget, sleep = budget, time.Sleep })

	// Act
	err = AtomicWriteFile(path, []byte("new"))

	// Assert
	if err == nil {
		t.Fatal("AtomicWriteFile replaced a file a reader never let go of")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("the error %q does not name %s", err, path)
	}
	if waited > transientBudget {
		t.Errorf("AtomicWriteFile waited %s before giving up, over its budget of %s", waited, transientBudget)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("the directory holds %d entries, want only the file (no temp file left)", len(entries))
	}
}

// A reader that held the file for 0.7 s cost a write 1.13 s: the pause between
// two tries doubled to 320 ms and then to half a second, so the write slept
// through the moment the reader let go. No pause is longer than longestPause,
// so a hold costs a write its own length and one pause at most.
func TestAHeldFileCostsAWriteItsHoldAndOnePauseAtMost(t *testing.T) {
	// Arrange
	path := filepath.Join(t.TempDir(), ".wake-queue")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	reader, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	const hold = 700 * time.Millisecond
	released := make(chan struct{})
	go func() {
		time.Sleep(hold)
		reader.Close()
		close(released)
	}()
	var waited, longest time.Duration
	sleep = func(wait time.Duration) {
		waited += wait
		longest = max(longest, wait)
		time.Sleep(wait)
	}
	t.Cleanup(func() { sleep = time.Sleep })

	// Act
	err = AtomicWriteFile(path, []byte("new"))
	<-released

	// Assert
	if err != nil {
		t.Fatalf("AtomicWriteFile while a reader held the file for %s: %v", hold, err)
	}
	if longest > 50*time.Millisecond {
		t.Errorf("the longest pause between two tries was %s, want 50 ms at most", longest)
	}
	if waited > hold+50*time.Millisecond {
		t.Errorf("the write waited %s on a hold of %s, want the hold and one pause of 50 ms at most", waited, hold)
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

// A folder's removal waits out another process holding a file in it, as a
// virus scanner holds one it just saw: the one silenced try each temporary
// folder got before 2026-10-10 left the folder behind.
func TestRemoveAllWaitsOutAReaderThatHoldsAFileInTheFolder(t *testing.T) {
	// Arrange
	dir := filepath.Join(t.TempDir(), "code-goblins-setup-1")
	if err := os.MkdirAll(filepath.Join(dir, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "nested", "install.ps1")
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
	err = RemoveAll(dir)
	<-released

	// Assert
	if err != nil {
		t.Fatalf("RemoveAll while a reader held a file in the folder for 1.5 s: %v", err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the folder is still there after RemoveAll: %v", err)
	}
}

// The wait is bounded, and what stays is named: a folder something never lets
// go of costs a removal at most transientBudget of waiting, and the error
// names the file that held it. A folder that is not there is no error.
func TestRemoveAllGivesUpOnAReaderThatNeverLetsGo(t *testing.T) {
	// Arrange
	dir := filepath.Join(t.TempDir(), "code-goblins-setup-1")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "install.ps1")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	reader, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	budget := transientBudget
	transientBudget = 300 * time.Millisecond
	var waited time.Duration
	sleep = func(wait time.Duration) {
		waited += wait
		time.Sleep(wait)
	}
	t.Cleanup(func() { transientBudget, sleep = budget, time.Sleep })

	// Act
	err = RemoveAll(dir)
	missing := RemoveAll(filepath.Join(dir, "never-made"))

	// Assert
	if err == nil {
		t.Fatal("RemoveAll removed a folder holding a file a reader never let go of")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("the error %q does not name %s", err, path)
	}
	if waited == 0 || waited > transientBudget {
		t.Errorf("RemoveAll waited %s before giving up, want some wait within its budget of %s", waited, transientBudget)
	}
	if missing != nil {
		t.Errorf("RemoveAll of a folder that is not there: %v", missing)
	}
}
