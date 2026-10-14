package verify

import (
	"os"
	"os/exec"
	"testing"
)

// A process a run in the heavy line started names the run's turn in its
// environment, as everything it starts in turn does, and one started
// outside a run names none.
func TestRunsInTurnReadsTheTurnAProcessIsStartedIn(t *testing.T) {
	for _, test := range []struct {
		name     string
		turn     string
		isInTurn bool
	}{
		{"started in a turn", "slot-0:4242:1", true},
		{"started outside a turn", "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			child := exec.Command("ping", "-n", "30", "127.0.0.1")
			child.Env = append(os.Environ(), TurnVariable+"="+test.turn)
			if err := child.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })

			// Act
			isInTurn := RunsInTurn(child.Process.Pid)

			// Assert
			if isInTurn != test.isInTurn {
				t.Errorf("RunsInTurn = %v, want %v", isInTurn, test.isInTurn)
			}
		})
	}
}
