package verify

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// recoveryRole names what this test binary is when a recovery test starts
// it: the controller of an execution, its worker, the worker's child, or a
// controller that leaves a worker outside its job.
const recoveryRole = "VERIFY_RECOVERY_ROLE"

// controlAnExecution runs one execution whose worker leaves a descendant
// running, in the admission store the test names, until it is killed.
func controlAnExecution() int {
	executor := Executor{
		Admission: Admission{Dir: os.Getenv("VERIFY_RECOVERY_ADMISSION"), Who: "a recovery fixture", Limit: time.Minute, Poll: 5 * time.Millisecond},
		// The descendant must outlive the run until the test kills its
		// controller, so the linger outlasts the test.
		Monitor: Monitor{Poll: 10 * time.Millisecond, Linger: time.Hour, Headroom: func() (Headroom, error) {
			return Headroom{PhysicalAvailable: 64 << 30, CommitAvailable: 64 << 30}, nil
		}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	executor.Run(ctx, execx.Request{Dir: os.Getenv("VERIFY_RECOVERY_ROOT"), Name: os.Args[0], Args: os.Args[1:], Env: append(os.Environ(), recoveryRole+"=worker")})
	return 0
}

func readExecution(t *testing.T, path string) ExecutionRecord {
	t.Helper()
	data, err := fsx.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var record ExecutionRecord
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	return record
}

func waitForFile(t *testing.T, path string, within time.Duration) []byte {
	t.Helper()
	for deadline := time.Now().Add(within); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
			return data
		}
	}
	t.Fatalf("%s did not appear within %s", path, within)
	return nil
}
