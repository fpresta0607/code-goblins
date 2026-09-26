package host

import (
	"errors"
	"os"
	"os/exec"
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

// A record left behind by a host that ended without removing it names no
// host once a later process has its pid: Close reports nothing wrong without
// waiting for that process to answer, and leaves it running.
func TestCloseTakesARecordWhosePidALaterProcessReusesAsEnded(t *testing.T) {
	stateDir := t.TempDir()
	record := Record{ID: "g1", Pipe: `\\.\pipe\cfo-host-g1`, Token: "0123", Version: Version, Started: time.Now().UTC()}
	later := exec.Command(os.Args[0], "sleep-child")
	if err := later.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		end(later.Process.Pid)
		_ = later.Wait()
	})
	record.HostPID, record.ChildPID = later.Process.Pid, later.Process.Pid
	if err := writeRecord(stateDir, record); err != nil {
		t.Fatal(err)
	}

	began := time.Now()
	err := Close(stateDir, record, 5*time.Second)
	took := time.Since(began)

	if err != nil {
		t.Errorf("Close: %v", err)
	}
	if took >= 5*time.Second {
		t.Errorf("Close took %s, want it back without waiting for the pid's process to answer", took)
	}
	if !running(later.Process.Pid) {
		t.Errorf("Close stopped pid %d, a process that is not its host", later.Process.Pid)
	}
}
