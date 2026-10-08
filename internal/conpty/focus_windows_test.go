package conpty

import (
	"testing"
)

// OpenConsole asks its terminal for focus reports as it starts, so a viewer
// that sends them, as xterm.js and Windows Terminal do, sends one each time
// its window gains or loses the focus. The console server takes each as a
// focus event, which a program reading text never reads, and never types
// it into the program.
func TestAViewersFocusReportsNeverReachTheProgramAsTypedInput(t *testing.T) {
	if problem := ConsoleHostProblem(); problem != nil {
		t.Fatalf("premise: consoles run on the embedded OpenConsole: %v", problem)
	}
	// Arrange
	console, s := startChild(t, Spec{Cols: 80, Rows: 25})
	s.waitFor(t, `\x1b\[\?1004h`)

	// Act: focus gained, lost and gained again, then a line.
	if _, err := console.Write([]byte("\x1b[I\x1b[O\x1b[I")); err != nil {
		t.Fatal(err)
	}
	typeLine(t, console, "hello")

	// Assert
	if got := s.waitFor(t, `got (.*)hello`); got[1] != "" {
		t.Fatalf("the program got %q typed before hello by a viewer's focus reports", got[1])
	}
}
