package onboarding

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

// keys is a ReadKey that returns each key in turn and then the end of input.
func keys(sequence ...Key) func() (Key, error) {
	return func() (Key, error) {
		if len(sequence) == 0 {
			return 0, io.EOF
		}
		key := sequence[0]
		sequence = sequence[1:]
		return key, nil
	}
}

// Enter accepts the marked choice, Up and Down move it and wrap, a choice's
// own letter accepts it at once, Escape goes back, and input that ends
// accepts nothing.
func TestAMenuAnswersToEnterTheArrowsALetterAndEscape(t *testing.T) {
	choices := []Choice{{Label: "Open the CFO terminal"}, {Label: "Open the board", Key: 'b'}, {Label: "Leave"}}
	for _, c := range []struct {
		name string
		keys []Key
		want int
		err  error
	}{
		{"Enter accepts the default", []Key{KeyEnter}, 0, nil},
		{"Down then Enter", []Key{KeyDown, KeyEnter}, 1, nil},
		{"Up wraps to the last", []Key{KeyUp, KeyEnter}, 2, nil},
		{"Down wraps to the first", []Key{KeyDown, KeyDown, KeyDown, KeyEnter}, 0, nil},
		{"a choice's letter", []Key{'b'}, 1, nil},
		{"a letter no choice has is ignored", []Key{'x', KeyEnter}, 0, nil},
		{"Escape goes back", []Key{KeyDown, KeyEscape}, 0, ErrBack},
		{"the end of input accepts nothing", nil, 0, ErrCancelled},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			menu := Menu{Output: io.Discard, ReadKey: keys(c.keys...)}

			// Act
			got, err := menu.Choose("Your CFO is running", choices, 0)

			// Assert
			if got != c.want || !errors.Is(err, c.err) {
				t.Errorf("Choose = %d, %v; want %d, %v", got, err, c.want, c.err)
			}
		})
	}
}

// Every screen says how to go on: its title, its choices with the default
// marked, Enter to continue, and the letter of a choice that has one.
func TestAMenuShowsItsDefaultAndTheKeysThatAnswerIt(t *testing.T) {
	// Arrange
	var output bytes.Buffer
	menu := Menu{Output: &output, ReadKey: keys(KeyEnter)}

	// Act
	_, err := menu.Choose("Your CFO is running\nHome  C:\\CodeGoblins", []Choice{{Label: "Open the CFO terminal"}, {Label: "Open the board", Key: 'b'}}, 0)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	shown := output.String()
	for _, want := range []string{"Your CFO is running", "Home  C:\\CodeGoblins", "  > Open the CFO terminal", "    Open the board", "Press Enter to continue   B: Open the board   Up/Down to choose   Esc to go back"} {
		if !strings.Contains(shown, want) {
			t.Errorf("the screen lacks %q:\n%q", want, shown)
		}
	}
}

// A menu with no choices, or a default that is none of them, is refused
// before anything is read.
func TestAMenuRefusesADefaultThatIsNoChoice(t *testing.T) {
	for name, selected := range map[string]int{"below": -1, "beyond": 2} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			menu := Menu{Output: io.Discard, ReadKey: func() (Key, error) {
				t.Error("read a key for a menu it refused")
				return 0, io.EOF
			}}

			// Act
			_, err := menu.Choose("Title", Labels("One", "Two"), selected)

			// Assert
			if err == nil || errors.Is(err, ErrCancelled) {
				t.Errorf("Choose error = %v, want the default refused", err)
			}
		})
	}
}

// A row wider than the console wraps onto more lines, and a redraw climbs
// back over every one of them, so the choices are drawn again in place rather
// than below the last ones.
func TestAMenuRedrawClimbsBackOverWrappedRows(t *testing.T) {
	// The hint is 60 columns; the rows are "    One" and the 34 columns of
	// four spaces and 30 x's.
	for _, c := range []struct {
		name  string
		width int
		want  string
	}{
		{"a console 20 columns wide", 20, "\x1b[7A"},
		{"a console wide enough for every row", 120, "\x1b[4A"},
		{"a console of no known width", 0, "\x1b[4A"},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			var output bytes.Buffer
			menu := Menu{Output: &output, ReadKey: keys(KeyDown, KeyEnter), Width: func() int { return c.width }}

			// Act
			_, err := menu.Choose("Title", Labels("One", strings.Repeat("x", 30)), 0)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if moves := strings.Count(output.String(), c.want); moves != 1 {
				t.Errorf("the redraw moved up with %q %d times in %q, want once", c.want, moves, output.String())
			}
		})
	}
}
