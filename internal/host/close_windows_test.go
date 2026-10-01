package host

import (
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"

	"golang.org/x/sys/windows"
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

// setCloseTerminal stands send in for Close's close request for one test.
func setCloseTerminal(t *testing.T, send func(*Client) error) {
	t.Helper()
	previous := closeTerminal
	closeTerminal = send
	t.Cleanup(func() { closeTerminal = previous })
}

// A host told /exit closes its end of the pipe as it ends, so a Close that
// reaches it in that moment has its request refused with "The pipe is being
// closed." (2026-10-01, the gate's spawn cleanup). The host ending is the
// close Close asked for: it reports nothing wrong and the record goes.
func TestCloseTakesAHostEndingAsItsRequestIsRefusedAsEnded(t *testing.T) {
	stateDir, record := launch(t)
	var refused error
	setCloseTerminal(t, func(client *Client) error {
		if err := client.CloseTerminal(); err != nil {
			return err
		}
		for deadline := time.Now().Add(20 * time.Second); running(record.HostPID) && time.Now().Before(deadline); {
			time.Sleep(20 * time.Millisecond)
		}
		refused = client.CloseTerminal()
		return refused
	})

	err := Close(stateDir, record, time.Second)

	if !pipeClosing(refused) {
		t.Fatalf("the premise failed: a request to the ended host was refused with %v, not a closing pipe", refused)
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

// A refused request proves nothing by itself: a host whose pipe refused it
// and that keeps running is still reported, once the wait for its end runs
// out, even with its record gone, since a host removes its record while it
// winds down and only its end proves the close; any other failure is
// reported at once.
func TestCloseReportsARefusedRequestWhoseHostKeepsRunning(t *testing.T) {
	closing := &os.PathError{Op: "write", Path: `\\.\pipe\code-goblins-host-test`, Err: windows.ERROR_NO_DATA}
	for _, test := range []struct {
		name            string
		refusal         error
		isRecordRemoved bool
		isWait          bool
	}{
		{"a closing pipe", closing, false, true},
		{"a closing pipe with the record gone", closing, true, true},
		{"any other failure", errors.New("host: the close request was not understood"), false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			stateDir, record := launch(t)
			t.Cleanup(func() { end(record.HostPID) })
			previousWait := closeEndWait
			closeEndWait = time.Second
			t.Cleanup(func() { closeEndWait = previousWait })
			setCloseTerminal(t, func(*Client) error {
				if test.isRecordRemoved {
					if err := os.Remove(recordPath(stateDir, record.ID)); err != nil {
						t.Error(err)
					}
				}
				return test.refusal
			})
			began := time.Now()

			err := Close(stateDir, record, time.Second)
			took := time.Since(began)

			if !errors.Is(err, test.refusal) {
				t.Fatalf("Close = %v, want the refusal %v", err, test.refusal)
			}
			if test.isWait && took < closeEndWait {
				t.Errorf("Close reported after %s, before the host's %s to end", took, closeEndWait)
			}
			if !test.isWait && took >= closeEndWait {
				t.Errorf("Close reported after %s, want at once", took)
			}
			if !running(record.HostPID) {
				t.Errorf("host pid %d ended, but nothing asked it to", record.HostPID)
			}
		})
	}
}

// A host can refuse the request while it is still running and end a moment
// later: Close waits for that end within its bound and reports nothing
// wrong.
func TestCloseWaitsForAHostThatEndsAfterRefusingItsRequest(t *testing.T) {
	stateDir, record := launch(t)
	isAliveAtRefusal := false
	setCloseTerminal(t, func(*Client) error {
		isAliveAtRefusal = running(record.HostPID)
		go func() {
			time.Sleep(300 * time.Millisecond)
			if closer, err := Dial(record); err == nil {
				_ = closer.CloseTerminal()
				_ = closer.Close()
			}
		}()
		return &os.PathError{Op: "write", Path: record.Pipe, Err: windows.ERROR_NO_DATA}
	})

	err := Close(stateDir, record, time.Second)

	if !isAliveAtRefusal {
		t.Fatal("the premise failed: the host had ended before its request was refused")
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

// Windows refuses to delete a file another process has open without delete
// sharing, as Go opens every file, so a reader of the record at the moment a
// host ends made the host's removal fail. Close then waited on a record that
// would never go and reported a host that had ended as one that did not. The
// host ended is what Close waits for, so it takes the host's end as its end
// and removes the record the host could not.
func TestCloseEndsAHostWhoseRecordAReaderHeldAsItEnded(t *testing.T) {
	stateDir, record := launch(t)
	reader, err := os.Open(recordPath(stateDir, record.ID))
	if err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	go func() {
		defer close(released)
		for deadline := time.Now().Add(20 * time.Second); running(record.HostPID) && time.Now().Before(deadline); {
			time.Sleep(20 * time.Millisecond)
		}
		_ = reader.Close()
	}()

	err = Close(stateDir, record, time.Second)
	<-released

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

// A reader that has the record open for a moment does not keep it from being
// removed: the removal is tried again until the reader lets go.
func TestRemoveRecordOutlastsABriefReader(t *testing.T) {
	stateDir := t.TempDir()
	record := Record{ID: "g1", Pipe: `\.\pipe\cfo-host-g1`, Token: "0123", Version: Version, HostPID: os.Getpid(), Started: time.Now().UTC()}
	if err := writeRecord(stateDir, record); err != nil {
		t.Fatal(err)
	}
	reader, err := os.Open(recordPath(stateDir, record.ID))
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(200 * time.Millisecond)
		_ = reader.Close()
	}()

	removeRecord(stateDir, record.ID, record.HostPID)

	if _, err := ReadRecord(stateDir, record.ID); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the record is still there: %v", err)
	}
}
