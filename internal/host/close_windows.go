package host

import (
	"errors"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/proc"
)

// Close ends the native terminal whose host launched is, the
// harness and everything it started, and waits for its host to end. It closes
// only that host: a terminal its id names that another host runs is left
// alone. A zero record launched nothing, and a host that has ended has
// nothing left to close; a running host that does not answer, as one under
// load can be busy, is dialed again for at most answerWait.
func Close(stateDir string, launched Record, answerWait time.Duration) error {
	if launched.HostPID == 0 {
		return nil
	}
	recorded := func() (bool, error) {
		record, err := ReadRecord(stateDir, launched.ID)
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return err == nil && record.HostPID == launched.HostPID, err
	}
	if still, err := recorded(); err != nil || !still {
		return err
	}
	deadline := time.Now().Add(answerWait)
	client, err := Dial(launched)
	for err != nil {
		if !Running(launched) {
			return nil
		}
		if still, readErr := recorded(); readErr == nil && !still {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the host of native terminal %s, pid %d, does not answer, so its harness may still be running: %w", launched.ID, launched.HostPID, err)
		}
		time.Sleep(100 * time.Millisecond)
		client, err = Dial(launched)
	}
	closeErr := client.CloseTerminal()
	_ = client.Close()
	if closeErr != nil {
		return closeErr
	}
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if still, err := recorded(); err == nil && !still {
			return nil
		}
	}
	return fmt.Errorf("the host of native terminal %s did not end", launched.ID)
}

// Running reports whether the host record names may still run: only a
// process Windows shows as ended, or as never started, does not, and neither
// does one that started after the host recorded itself, which reuses the pid
// of a host that ended without removing its record.
func Running(record Record) bool {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(record.HostPID))
	if err != nil {
		return !errors.Is(err, windows.ERROR_INVALID_PARAMETER)
	}
	defer windows.CloseHandle(handle)
	var code uint32
	if err := windows.GetExitCodeProcess(handle, &code); err == nil && code != stillActive {
		return false
	}
	started, known := proc.StartTime(record.HostPID)
	return !known || !started.After(record.Started)
}

// stillActive is the exit code Windows reports for a process that runs.
const stillActive = 259
