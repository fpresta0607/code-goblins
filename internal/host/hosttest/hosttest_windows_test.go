package hosttest

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/host"
)

// A host renames its record into place, and until Windows has closed the
// handle that renamed it the record is open for deletion. Rewrite replaces
// the record all the same, where the plain write the fixtures made before
// 2026-10-10 is refused.
func TestRewriteReplacesARecordTheRenameThatPlacedItStillHolds(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()
	path := filepath.Join(stateDir, "hosts", "cfo.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(host.Record{ID: "cfo", Pipe: `\\.\pipe\hosttest-nobody-serves-this`, Token: "token", Version: host.Version, HostPID: os.Getpid(), ChildPID: 4, Started: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	name, err := windows.UTF16PtrFromString(path)
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
	// test would pass with nothing held against Rewrite.
	if plain, err := os.OpenFile(path, os.O_WRONLY, 0o600); !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		if err == nil {
			_ = plain.Close()
		}
		t.Fatalf("a plain open to write the held record = %v, want a sharing violation", err)
	}

	// Act
	Rewrite(t, stateDir, "cfo", func(record *host.Record) { record.ChildPID = os.Getpid() })

	// Assert
	record, err := host.ReadRecord(stateDir, "cfo")
	if err != nil {
		t.Fatal(err)
	}
	if record.ChildPID != os.Getpid() {
		t.Errorf("the record names program pid %d, want this process, %d", record.ChildPID, os.Getpid())
	}
}
