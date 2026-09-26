package monitor

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// A status call that exits but leaves a process holding its output pipe held
// the probe, and the supervisor loop waiting on it, until that process ended.
func TestExecGateProberReturnsWhenTheStatusCallLeavesAProcessHoldingItsOutput(t *testing.T) {
	fixture := newPollFixture(t)
	fixture.standIn("no-mistakes.exe")
	pidFile := filepath.Join(fixture.root, "child.pid")
	t.Setenv("PATH", fixture.bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CFO_POLL_STANDIN", "gate")
	t.Setenv("CFO_GATE_STATUS", wedgedStatus)
	t.Setenv("CFO_GATE_CHILD_PID", pidFile)
	t.Cleanup(func() {
		data, err := os.ReadFile(pidFile)
		if err != nil {
			return
		}
		if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
			if process, err := os.FindProcess(pid); err == nil {
				_ = process.Kill()
			}
		}
	})

	start := time.Now()
	sample, err := ExecGateProber{}.InspectGate(context.Background(), state.TaskMeta{Worktree: fixture.dir("work")})
	elapsed := time.Since(start)

	if elapsed > 10*time.Second {
		t.Fatalf("InspectGate took %s, held by the process the status call left running", elapsed)
	}
	if err != nil && !sample.Active {
		t.Fatalf("InspectGate = %+v, %v; want the status it printed read", sample, err)
	}
	if !sample.Active || sample.Step != "ci" {
		t.Errorf("sample = %+v, want the active ci step the status call printed", sample)
	}
}
