//go:build linux || darwin

package supervisor

import (
	"os"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/proc"
)

// thisHostInstance is this test process's instance, which a host record
// that names this process as its host must carry for host.Running to read
// it as running here.
func thisHostInstance(t *testing.T) string {
	t.Helper()
	instance, err := proc.Instance(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	return instance
}
