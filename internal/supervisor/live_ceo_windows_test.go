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

// writeRegistration writes primary.json naming process as the CEO.
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

// A registration naming a running process is the live CEO, at the Herdr
// address it registered.
func TestLiveCEOReturnsTheRegisteredEndpointOfARunningProcess(t *testing.T) {
	stateDir := t.TempDir()
	writeRegistration(t, stateDir, thisProcess(t))

	endpoint, live := LiveCEO(stateDir)

	want := herdr.Endpoint{Target: herdr.Target{Session: "isolated", Pane: "w1:p1"}, WorkspaceID: "w1", TabID: "w1:t1", PaneID: "w1:p1"}
	if !live || endpoint != want {
		t.Fatalf("LiveCEO = %+v, %v; want %+v, true", endpoint, live, want)
	}
}

// Anything short of a registration naming a running process is no live CEO.
func TestLiveCEOIsFalseWithoutARunningRegisteredProcess(t *testing.T) {
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

			if endpoint, live := LiveCEO(stateDir); live {
				t.Fatalf("LiveCEO = %+v, true; want no live CEO", endpoint)
			}
		})
	}
}

// A goblin the supervisor starts, resumes or switches records the registered
// CEO's session as its parent, since no session of a CEO reaches that start:
// the live CEO's session and harness, and none while no CEO runs.
func TestRegisteredCEOSessionIsTheLiveCEOsSessionAndNoneWithoutOne(t *testing.T) {
	ended := thisProcess(t)
	ended.PID = 0x7FFFFFF0
	for name, test := range map[string]struct {
		process                  lock.Info
		isRegistered             bool
		wantSession, wantHarness string
	}{
		"a running CEO":   {process: thisProcess(t), isRegistered: true, wantSession: "the-ceo-session", wantHarness: "claude"},
		"an ended CEO":    {process: ended, isRegistered: true},
		"no registration": {},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			stateDir := t.TempDir()
			if test.isRegistered {
				test.process.Session = "the-ceo-session"
				writeRegistration(t, stateDir, test.process)
			}

			// Act
			session, harness := RegisteredCEOSession(stateDir)

			// Assert
			if session != test.wantSession || harness != test.wantHarness {
				t.Fatalf("RegisteredCEOSession = %q/%q, want %q/%q", session, harness, test.wantSession, test.wantHarness)
			}
		})
	}
}
