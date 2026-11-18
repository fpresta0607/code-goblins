package supervisor

import (
	"os"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/proc"
)

// The record primary.json keeps of the CEO's process proves that process is
// running, which every delivery and caller check asks of it: on Linux and
// macOS only a record that names the process's instance does.
func TestARegisteredProcessIsProvenAliveByItsRecord(t *testing.T) {
	// Arrange
	ancestry, err := proc.Ancestry(os.Getpid(), 1)
	if err != nil || len(ancestry) == 0 {
		t.Fatalf("this process could not be read: %v", err)
	}
	self := ancestry[0]

	// Act
	record, err := registeredProcess(self, "the-ceo-session")

	// Assert
	if err != nil {
		t.Fatalf("registeredProcess = %v, want this process recorded", err)
	}
	if !record.VerifiedAlive() {
		t.Fatalf("the record %+v does not prove this running process alive", record)
	}
	if record.PID != self.PID || record.OwnerPID != self.PID || record.Session != "the-ceo-session" || !record.Start.Equal(self.Start) {
		t.Fatalf("the record %+v does not name process %d of session the-ceo-session started at %s", record, self.PID, self.Start)
	}
}
