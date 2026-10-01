package onboarding

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"
)

// ErrNoConsole says the quick start has no console to ask in.
var ErrNoConsole = errors.New("the quick start asks in a terminal, and this command has none; run goblins in a terminal window")

// Key is one key a menu reads: one of the named keys below, or a letter as
// its lower-case rune.
type Key rune

// The keys a menu acts on besides letters. They sit outside Unicode, so no
// letter is one of them.
const (
	KeyEnter Key = -(iota + 1)
	KeyUp
	KeyDown
	KeyEscape
)

// Choice is one row of a menu. Key, when set, is a letter that accepts the
// row at once, and the row says so.
type Choice struct {
	Label string
	Key   Key
}

// Menu shows a screen that waits on the person: a title, the choices with one
// marked as the default, and the keys that answer it. Enter accepts the marked
// choice, so every screen has one clear way on.
type Menu struct {
	Output io.Writer
	// ReadKey waits for the next key. An error means no key can come, as at
	// the end of input.
	ReadKey func() (Key, error)
}

// The quick start's own greens, the board's accent and mint.
const (
	menuAccent = "\x1b[38;2;0;229;155m"
	menuMarked = "\x1b[48;2;15;24;32m\x1b[38;2;110;231;183m"
	menuReset  = "\x1b[0m"
)

// Choose shows title above choices with selected marked, and returns the
// choice the person accepts: the marked one with Enter, moved with Up and
// Down, or a choice's own letter. Escape returns ErrBack. When no key can be
// read it returns ErrCancelled, so input that ended never accepts a default.
func (m Menu) Choose(title string, choices []Choice, selected int) (int, error) {
	if len(choices) == 0 || selected < 0 || selected >= len(choices) {
		return 0, errors.New("a menu needs choices and a default among them")
	}
	heading, detail, _ := strings.Cut(title, "\n")
	fmt.Fprintf(m.Output, "\n%s%s%s\n", menuAccent, heading, menuReset)
	if detail != "" {
		fmt.Fprintf(m.Output, "%s\n", detail)
	}
	fmt.Fprintln(m.Output)
	hint := "Press Enter to continue"
	for _, choice := range choices {
		if choice.Key != 0 {
			hint += fmt.Sprintf("   %c: %s", unicode.ToUpper(rune(choice.Key)), choice.Label)
		}
	}
	hint += "   Up/Down to choose   Esc to go back"
	for {
		for index, choice := range choices {
			// \x1b[2K clears the row, so a redraw leaves nothing of the last.
			if index == selected {
				fmt.Fprintf(m.Output, "\x1b[2K%s  > %s%s\n", menuMarked, choice.Label, menuReset)
			} else {
				fmt.Fprintf(m.Output, "\x1b[2K    %s\n", choice.Label)
			}
		}
		fmt.Fprintf(m.Output, "\x1b[2K\n\x1b[2K%s\n", hint)
		key, err := m.ReadKey()
		if err != nil {
			return 0, ErrCancelled
		}
		switch key {
		case KeyEnter:
			return selected, nil
		case KeyEscape:
			return 0, ErrBack
		case KeyUp:
			selected = (selected + len(choices) - 1) % len(choices)
		case KeyDown:
			selected = (selected + 1) % len(choices)
		default:
			for index, choice := range choices {
				if choice.Key != 0 && choice.Key == key {
					return index, nil
				}
			}
		}
		// Back to the first choice's row, to draw the choices again in place.
		fmt.Fprintf(m.Output, "\x1b[%dA", len(choices)+2)
	}
}

// Labels are choices with no letters of their own.
func Labels(labels ...string) []Choice {
	choices := make([]Choice, len(labels))
	for index, label := range labels {
		choices[index] = Choice{Label: label}
	}
	return choices
}
