package host

import (
	"errors"
	"os"
	"os/exec"
	"slices"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/proc/proctest"
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
		for deadline := time.Now().Add(time.Minute); running(record.HostPID) && time.Now().Before(deadline); {
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
			closeEndWait = time.Minute
			if test.isWait {
				closeEndWait = time.Second
			}
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
// waiting for that process to answer, and leaves it running. That holds for a
// process this user may not open too, as a system service's conhost.exe that
// took a host's pid after a restart was (issue #1206).
func TestCloseTakesARecordWhosePidALaterProcessReusesAsEnded(t *testing.T) {
	for name, start := range map[string]func(*testing.T) int{
		"a later process":                        startSleepChild,
		"a later process this user may not open": startUnopenableSleepChild,
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			stateDir := t.TempDir()
			record := Record{ID: "g1", Pipe: `\\.\pipe\cfo-host-g1`, Token: "0123", Version: Version, Started: time.Now().UTC().Add(-time.Second)}
			later := start(t)
			record.HostPID, record.ChildPID = later, later
			if err := writeRecord(stateDir, record); err != nil {
				t.Fatal(err)
			}

			// Act
			began := time.Now()
			err := Close(stateDir, record, 2*time.Minute)
			took := time.Since(began)

			// Assert
			if err != nil {
				t.Errorf("Close: %v", err)
			}
			if took >= time.Minute {
				t.Errorf("Close took %s, want it back without waiting for the pid's process to answer", took)
			}
			if !isStillRunning(t, later) {
				t.Errorf("Close stopped pid %d, a process that is not its host", later)
			}
		})
	}
}

// A record names its host by pid, and Windows gives an ended host's pid to a
// later process, so the pid counts as the host only while the process holding
// it started no later than the host recorded itself. A process this user may
// not open is judged the same way, by the start the machine's process list
// gives without opening it: before issue #1206, Running read any such process
// as the host, so a record whose pid a system service's process took after a
// restart could neither be cleaned up nor restarted.
func TestRunningKnowsAHostByWhenItsProcessStarted(t *testing.T) {
	// Recorded before it ran, so a process that took its pid since started
	// after the record too.
	exitedRecorded := time.Now().UTC()
	exited := exec.Command(os.Args[0], "-test.run=^$")
	if err := exited.Run(); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name      string
		arrange   func(*testing.T) (pid int, recorded time.Time)
		isRunning bool
	}{
		{"a pid no process holds", func(*testing.T) (int, time.Time) { return exited.ProcessState.Pid(), exitedRecorded }, false},
		{"a pid a later process holds", func(t *testing.T) (int, time.Time) {
			recorded := time.Now().UTC().Add(-time.Second)
			return startSleepChild(t), recorded
		}, false},
		{"a pid a later process this user may not open holds", func(t *testing.T) (int, time.Time) {
			recorded := time.Now().UTC().Add(-time.Second)
			return startUnopenableSleepChild(t), recorded
		}, false},
		{"its host, answering", func(t *testing.T) (int, time.Time) {
			_, record := launch(t)
			return record.HostPID, record.Started
		}, true},
		{"its host, answering nobody", func(*testing.T) (int, time.Time) { return os.Getpid(), time.Now().UTC() }, true},
		{"its host, which this user may not open", func(t *testing.T) (int, time.Time) {
			pid := startUnopenableSleepChild(t)
			return pid, time.Now().UTC().Add(time.Second)
		}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			pid, recorded := test.arrange(t)
			record := Record{ID: "g1", Pipe: `\\.\pipe\cfo-host-g1`, Token: "0123", Version: Version, HostPID: pid, Started: recorded}

			// Act
			isRunning := Running(record)

			// Assert
			if isRunning != test.isRunning {
				t.Errorf("Running(%+v) = %v, want %v", record, isRunning, test.isRunning)
			}
		})
	}
}

// startSleepChild starts this test binary as a process that sleeps until the
// test ends it, and returns its pid.
func startSleepChild(t *testing.T) int {
	t.Helper()
	child := exec.Command(os.Args[0], "sleep-child")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = child.Process.Kill()
		_ = child.Wait()
	})
	return child.Process.Pid
}

// startUnopenableSleepChild is startSleepChild for a process this user may
// not open.
func startUnopenableSleepChild(t *testing.T) int {
	t.Helper()
	return proctest.StartUnopenable(t, execx.Command(os.Args[0], "sleep-child"))
}

// isStillRunning reports whether pid is in the machine's process list, which
// names a process this user may not open too.
func isStillRunning(t *testing.T, pid int) bool {
	t.Helper()
	processes, err := proc.Processes()
	if err != nil {
		t.Fatal(err)
	}
	return slices.ContainsFunc(processes, func(process proc.Entry) bool { return process.PID == pid })
}

// Windows refuses to delete a file another process has open without delete
// sharing, as Go opens every file, so a reader of the record at the moment a
// host ends made the host's removal fail. Close then waited on a record that
// would never go and reported a host that had ended as one that did not. The
// host ended is what Close waits for, so it takes the host's end as its end
// and removes the record the host could not.
//
// The reader lets go when Running says the host has ended, which is the
// moment Close starts its own removal, tried for removeWait. Let go by any
// later sign of the host's end, the reader can outlast that removal: one let
// go 2.1 seconds late left the record behind with Close returning nil, which
// is how this test failed in CI on 2026-10-01.
func TestCloseEndsAHostWhoseRecordAReaderHeldAsItEnded(t *testing.T) {
	stateDir, record := launch(t)
	reader, err := os.Open(recordPath(stateDir, record.ID))
	if err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	var heldErr error
	go func() {
		defer close(released)
		for deadline := time.Now().Add(time.Minute); Running(record) && time.Now().Before(deadline); {
			time.Sleep(20 * time.Millisecond)
		}
		_, heldErr = ReadRecord(stateDir, record.ID)
		_ = reader.Close()
	}()

	err = Close(stateDir, record, time.Second)
	<-released

	if heldErr != nil {
		t.Fatalf("the premise failed: the record the reader held was not there as its host ended: %v", heldErr)
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
