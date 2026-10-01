package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// cfo notify appends a status line beside its wake record, and another
// process holding the status file for a moment, as a scan or a replace in
// flight does, must cost that notify a wait, not the notify.
func TestAppendStatusWaitsOutAHandleThatHoldsTheFile(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	if err := AppendStatus(dir, "task", "working: first"); err != nil {
		t.Fatal(err)
	}
	name, err := windows.UTF16PtrFromString(filepath.Join(dir, "task.status"))
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	go func() {
		time.Sleep(1500 * time.Millisecond)
		windows.CloseHandle(handle)
		close(released)
	}()

	// Act
	err = AppendStatus(dir, "task", "working: second")
	<-released

	// Assert
	if err != nil {
		t.Fatalf("AppendStatus while the status file was held for 1.5 s: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "task.status"))
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(string(data), "\n"); lines != 2 {
		t.Errorf("the status file holds %d lines, want 2", lines)
	}
}
