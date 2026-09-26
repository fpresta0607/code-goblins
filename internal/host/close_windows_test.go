package host

import (
	"errors"
	"os"
	"testing"
	"time"
)

// Close ends only the host it was handed: a terminal of the same id that
// another host runs is left running, and the host it was handed ends with
// its terminal and its record.
func TestCloseEndsOnlyTheHostItWasHanded(t *testing.T) {
	stateDir, record := launch(t)
	other := record
	other.HostPID = os.Getpid()

	otherErr := Close(stateDir, other, time.Second)
	stillRunning := running(record.HostPID)
	err := Close(stateDir, record, time.Second)

	if otherErr != nil || !stillRunning {
		t.Errorf("Close of another host's record = %v, and the terminal's host running = %v; want it left alone", otherErr, stillRunning)
	}
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !exited(record.HostPID) {
		t.Errorf("host pid %d is still running after its terminal closed", record.HostPID)
	}
	if _, err := ReadRecord(stateDir, record.ID); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the record is still there: %v", err)
	}
}
