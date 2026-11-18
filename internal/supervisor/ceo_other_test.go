//go:build !windows

package supervisor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

// On Linux and macOS a reader of primary.json reads the CEO that Register
// records: a process running under the registered harness, written as
// Register writes it, is proven to run under the registered CEO.
func TestAProcessUnderTheRegisteredCEOIsProvenSoByPrimaryJSON(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()
	ancestry, err := proc.Ancestry(os.Getpid(), 1)
	if err != nil || len(ancestry) == 0 {
		t.Fatalf("this process could not be read: %v", err)
	}
	process, err := registeredProcess(ancestry[0], "the-ceo-session")
	if err != nil {
		t.Fatalf("registeredProcess = %v, want this process recorded", err)
	}
	data, err := json.Marshal(primaryRegistration{Agent: "claude", Host: NativeCEOTerminal, Process: process})
	if err != nil {
		t.Fatal(err)
	}
	staged, err := fsx.Stage(filepath.Join(stateDir, "primary.json"), data)
	if err != nil {
		t.Fatal(err)
	}
	if err := staged.CommitUnheld(); err != nil {
		t.Fatal(err)
	}

	// Act
	underCEO := RunsUnderRegisteredCEO(stateDir)

	// Assert
	if !underCEO {
		t.Fatalf("RunsUnderRegisteredCEO = false for the registered process itself, recorded as %s", data)
	}
}
