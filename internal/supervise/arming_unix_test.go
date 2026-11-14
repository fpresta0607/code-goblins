//go:build linux || darwin

package supervise

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/proc"
)

// A firing that is killed takes no sign down: off Windows its ticket stays in
// the arming file, which nothing but the next look at it reads past. The
// turn-end guard waits for a firing only while that firing runs.
func TestAFiringThatEndedWithItsSignUpIsNotArming(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	firing := lastingProgram(30)
	if err := firing.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = firing.Process.Kill()
		_ = firing.Wait()
	})
	instance, err := proc.Instance(firing.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	// The sign's file is the line of tickets lock.Show keeps off Windows, one
	// "<pid> <instance> <serial>" to a line.
	ticket := fmt.Sprintf("%d %s 1\n", firing.Process.Pid, instance)
	if err := os.WriteFile(filepath.Join(dir, armingFile), []byte(ticket), 0o644); err != nil {
		t.Fatal(err)
	}
	if !AutoarmArming(dir) {
		t.Fatal("the ticket of a firing that runs does not read as arming, so the test proves nothing")
	}

	// Act
	_ = firing.Process.Kill()
	_ = firing.Wait()
	isArming := AutoarmArming(dir)

	// Assert
	if isArming {
		t.Error("a firing that ended with its sign up still reads as arming")
	}
}
