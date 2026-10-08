package conpty

import (
	"regexp"
	"strings"
	"testing"
)

// answerAsXterm types, for every device attributes query in text, the
// answer xterm.js types: a viewer of the terminal, as the board's is.
func answerAsXterm(t *testing.T, console *Console, text string) {
	t.Helper()
	for range strings.Count(text, "\x1b[c") {
		if _, err := console.Write([]byte("\x1b[?1;2c")); err != nil {
			t.Fatal(err)
		}
	}
}

// OpenConsole asks its terminal for its device attributes as it starts, and
// takes the first answer typed into it as the answer; any later one reaches
// the program as if typed. The board shows a terminal to each viewer by
// replaying what it wrote, so every viewer that answered the start's query
// would type the answer into the program.
func TestAViewerReplayingTheTerminalTypesNothingIntoItsProgram(t *testing.T) {
	if problem := ConsoleHostProblem(); problem != nil {
		t.Fatalf("premise: consoles run on the embedded OpenConsole: %v", problem)
	}
	// Arrange
	console, s := startChild(t, Spec{Cols: 80, Rows: 25})
	s.mu.Lock()
	replayed := s.text.String()
	s.mu.Unlock()

	// Act: two viewers, each shown the terminal from its start.
	answerAsXterm(t, console, replayed)
	answerAsXterm(t, console, replayed)
	typeLine(t, console, "hello")

	// Assert
	if got := s.waitFor(t, `got (.*)hello`); got[1] != "" {
		t.Fatalf("the program got %q typed before hello by viewers answering the terminal's start", got[1])
	}
}

// A program that asks its terminal for its device attributes gets the answer
// its viewer types, as it would in any terminal. OpenConsole would take the
// first answer typed as the answer to its own question unless the console
// had answered that already.
func TestAProgramsOwnDeviceAttributesQueryGetsItsViewersAnswer(t *testing.T) {
	if problem := ConsoleHostProblem(); problem != nil {
		t.Fatalf("premise: consoles run on the embedded OpenConsole: %v", problem)
	}
	// Arrange
	console, s := startChild(t, Spec{Cols: 80, Rows: 25})
	typeLine(t, console, "ask")
	s.waitFor(t, `asking\x1b\[c`)

	// Act: the viewer answers the program's question.
	answerAsXterm(t, console, "\x1b[c")
	typeLine(t, console, "answered")

	// Assert
	s.waitFor(t, regexp.QuoteMeta("got \x1b[?1;2canswered"))
}
