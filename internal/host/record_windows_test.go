package host

import (
	"errors"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// A host puts its record in place with a rename, and until Windows has
// closed the handle that renamed it the record is open for deletion. A plain
// open to write is refused meanwhile, as a test fixture's was in main's CI on
// 2026-10-10 (run 38054286836). The fleet's own reader and writer are not:
// ReadRecord shares deletion, and writeRecord renames over the record.
func TestARecordIsReadAndReplacedWhileTheRenameThatPlacedItStillHoldsIt(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()
	first := Record{ID: "g1", Pipe: `\\.\pipe\cfo-host-g1`, Token: "first", Version: Version, HostPID: os.Getpid(), Started: time.Now().UTC()}
	if err := writeRecord(stateDir, first); err != nil {
		t.Fatal(err)
	}
	name, err := windows.UTF16PtrFromString(recordPath(stateDir, first.ID))
	if err != nil {
		t.Fatal(err)
	}
	// Go's rename opens the file it renames for deletion and shares reading,
	// writing and deletion (Renameat in internal/syscall/windows).
	renamer, err := windows.CreateFile(name, windows.DELETE|windows.SYNCHRONIZE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = windows.CloseHandle(renamer) })
	// The premise: this hold refuses a plain write. Were it to stop, the
	// test would pass with nothing held against the reader and the writer.
	if plain, err := os.OpenFile(recordPath(stateDir, first.ID), os.O_WRONLY, 0o600); !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		if err == nil {
			_ = plain.Close()
		}
		t.Fatalf("a plain open to write the held record = %v, want a sharing violation", err)
	}
	next := first
	next.Token = "next"

	// Act
	read, readErr := ReadRecord(stateDir, first.ID)
	writeErr := writeRecord(stateDir, next)

	// Assert
	if readErr != nil || read.Token != first.Token {
		t.Errorf("ReadRecord of the held record = token %q, %v; want %q", read.Token, readErr, first.Token)
	}
	if writeErr != nil {
		t.Fatalf("writeRecord over the held record: %v", writeErr)
	}
	if replaced, err := ReadRecord(stateDir, first.ID); err != nil || replaced.Token != next.Token {
		t.Errorf("the record after the write = token %q, %v; want %q", replaced.Token, err, next.Token)
	}
}
