package supervisor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/lock"
)

// writeRegistration writes primary.json naming process as the CFO.
func writeRegistration(t *testing.T, stateDir string, process lock.Info) {
	t.Helper()
	primary := primaryRegistration{Target: herdr.Target{Session: "isolated", Pane: "w1:p1"}, Workspace: "w1", Tab: "w1:t1", Agent: "claude", Terminal: "test-terminal", Process: process}
	data, err := json.Marshal(primary)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "primary.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}

// thisProcess is the lock.Info of the running test process.
func thisProcess(t *testing.T) lock.Info {
	t.Helper()
	dir := t.TempDir()
	process, err := lock.Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Release(dir) })
	return *process
}

// A registration naming a running process is the live CFO, at the Herdr
// address it registered.
func TestLiveCFOReturnsTheRegisteredEndpointOfARunningProcess(t *testing.T) {
	stateDir := t.TempDir()
	writeRegistration(t, stateDir, thisProcess(t))

	endpoint, live := LiveCFO(stateDir)

	want := herdr.Endpoint{Target: herdr.Target{Session: "isolated", Pane: "w1:p1"}, WorkspaceID: "w1", TabID: "w1:t1", PaneID: "w1:p1"}
	if !live || endpoint != want {
		t.Fatalf("LiveCFO = %+v, %v; want %+v, true", endpoint, live, want)
	}
}

// Anything short of a registration naming a running process is no live CFO.
func TestLiveCFOIsFalseWithoutARunningRegisteredProcess(t *testing.T) {
	for name, arrange := range map[string]func(t *testing.T, stateDir string){
		"no registration": func(*testing.T, string) {},
		"an invalid registration": func(t *testing.T, stateDir string) {
			if err := os.WriteFile(filepath.Join(stateDir, "primary.json"), []byte(`{"target":`), 0600); err != nil {
				t.Fatal(err)
			}
		},
		"a process that has ended": func(t *testing.T, stateDir string) {
			process := thisProcess(t)
			process.PID = 0x7FFFFFF0
			writeRegistration(t, stateDir, process)
		},
		"a recycled pid": func(t *testing.T, stateDir string) {
			process := thisProcess(t)
			process.Start = process.Start.Add(-time.Hour)
			writeRegistration(t, stateDir, process)
		},
	} {
		t.Run(name, func(t *testing.T) {
			stateDir := t.TempDir()
			arrange(t, stateDir)

			if endpoint, live := LiveCFO(stateDir); live {
				t.Fatalf("LiveCFO = %+v, true; want no live CFO", endpoint)
			}
		})
	}
}
