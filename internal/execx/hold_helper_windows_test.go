package execx

import (
	"os"
	"testing"
)

// holdHelper opens the helper process pid and returns how to wait for its
// end. The open handle keeps its ID from passing to another program, and
// Windows lets any process wait on a process it has open.
func holdHelper(t *testing.T, pid int) (awaitEnd func()) {
	t.Helper()
	helper, err := os.FindProcess(pid)
	if err != nil {
		t.Fatalf("FindProcess(%d): %v", pid, err)
	}
	return func() {
		t.Helper()
		if _, err := helper.Wait(); err != nil {
			t.Fatalf("waiting for helper %d to end: %v", pid, err)
		}
	}
}
