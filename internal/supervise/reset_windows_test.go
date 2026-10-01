package supervise

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// The turnend-guard hook resets the rewake budget on a quiet Stop. Another
// process holding the budget record for a moment, as a scan does, must cost
// that reset a wait, not leave the budget half reset.
func TestResetBudgetWaitsOutAHandleThatHoldsTheRecord(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	if _, err := ChargeBudget(dir, "session"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, budgetFile)
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
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
	err = ResetBudget(dir)
	<-released

	// Assert
	if err != nil {
		t.Fatalf("ResetBudget while the budget record was held for 1.5 s: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the budget record is still there after ResetBudget: %v", err)
	}
}
