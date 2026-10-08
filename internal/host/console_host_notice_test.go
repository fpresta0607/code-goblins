package host

import (
	"errors"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/wake"
)

// A home whose terminals run on the system conhost tells the CFO once, with
// why, however many of its terminals start so.
func TestATerminalOnTheSystemConhostWakesTheCFOOnceWithWhy(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()
	problem := errors.New("the embedded OpenConsole cannot be used, so terminals run on the system conhost: Access is denied.")

	// Act
	for range 2 {
		if err := reportConsoleHostProblem(stateDir, problem); err != nil {
			t.Fatal(err)
		}
	}

	// Assert
	records, err := wake.Pending(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Kind != "check" || records[0].Key != "conpty" || !strings.HasPrefix(records[0].Detail, "console_host: ") || !strings.Contains(records[0].Detail, problem.Error()) {
		t.Fatalf("wakes = %+v, want one check wake keyed conpty that says %q", records, problem)
	}
	if episode, err := wake.ReadEpisode(stateDir); err != nil || episode.Gen == 0 {
		t.Fatalf("episode = %+v, %v; want the wake published to the CFO", episode, err)
	}
}
