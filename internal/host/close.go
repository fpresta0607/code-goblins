//go:build windows || linux || darwin

package host

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/fpresta0607/code-goblins/internal/awake"
)

// Close ends the native terminal or stream session whose host launched is,
// the harness and everything it started, and waits for its host to end. A
// stream session's harness is given streamCloseWait to end by itself first,
// within closeEndWait. It closes
// only that host: a terminal its id names that another host runs is left
// alone. A zero record launched nothing, and a host that has ended has
// nothing left to close; a running host that does not answer, as one under
// load can be busy, is dialed again for at most answerWait. Only time the
// machine ran spends answerWait and closeEndWait (awake), since a pause of the
// whole machine stops the host too.
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
	answering, cancelAnswering := awake.WithTimeout(context.Background(), answerWait)
	defer cancelAnswering()
	client, err := reach(launched)
	for err != nil {
		if !Running(launched) {
			return nil
		}
		if still, readErr := recorded(); readErr == nil && !still {
			return nil
		}
		if answering.Err() != nil {
			return fmt.Errorf("the host of native terminal %s, pid %d, does not answer, so its harness may still be running: %w", launched.ID, launched.HostPID, err)
		}
		time.Sleep(100 * time.Millisecond)
		client, err = reach(launched)
	}
	closeErr := closeTerminal(client)
	_ = client.Close()
	// A host already ending, as one told /exit is, closes its end of the pipe
	// before it reads the request, so the request fails to send. The host's
	// end proves the close as it does for a request that went through; any
	// other failure is returned as it is.
	if closeErr != nil && !pipeClosing(closeErr) {
		return closeErr
	}
	ending, cancelEnding := awake.WithTimeout(context.Background(), closeEndWait)
	defer cancelEnding()
	for ; ending.Err() == nil; time.Sleep(100 * time.Millisecond) {
		// A host that took the request removes its record as it ends. After
		// a refused request the record proves nothing, since a host removes
		// it while winding down and another can replace it: only this host's
		// end does.
		if still, err := recorded(); err == nil && !still && closeErr == nil {
			return nil
		}
		// A host whose removal a reader kept refusing ends with its record
		// still there: its end is what this waits for, and the record goes
		// with it.
		if !Running(launched) {
			removeRecord(stateDir, launched.ID, launched.HostPID)
			return nil
		}
	}
	if closeErr != nil {
		return fmt.Errorf("the host of native terminal %s did not end after its pipe refused the close request: %w", launched.ID, closeErr)
	}
	return fmt.Errorf("the host of native terminal %s did not end", launched.ID)
}

// closeEndWait bounds the wait for a host to end once it was asked to close.
var closeEndWait = 30 * time.Second

// closeTerminal sends the close request; a test stands in for it to fail the
// request the way a host already closing its pipe does.
var closeTerminal = (*Client).CloseTerminal
