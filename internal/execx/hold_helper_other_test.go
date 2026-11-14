//go:build !windows

package execx

import (
	"errors"
	"syscall"
	"testing"
	"time"
)

// holdHelper returns how to wait for the end of the helper process pid.
// Only a parent waits for a process here, and Start's own wait collects the
// helper, so this waits until the system no longer has the helper's ID.
func holdHelper(t *testing.T, pid int) (awaitEnd func()) {
	t.Helper()
	return func() {
		t.Helper()
		for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
				return
			}
		}
		t.Fatalf("helper %d did not end within a minute", pid)
	}
}
