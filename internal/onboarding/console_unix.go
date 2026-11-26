//go:build linux || darwin

package onboarding

import (
	"io"
	"os"
	"unicode"
	"unicode/utf8"

	"github.com/fpresta0607/code-goblins/internal/pty"
)

// ConsoleWidth is the width in columns of this process's terminal, or 0 when
// its output is not a terminal.
func ConsoleWidth() int {
	cols, _, err := pty.Size(int(os.Stdout.Fd()))
	if err != nil {
		return 0
	}
	return cols
}

// AskConsole shows a step in this process's terminal and reads its keys in
// raw mode, one at a time and unechoed. The terminal's mode is put back when
// it returns. Without a terminal it returns ErrNoConsole and accepts nothing.
func AskConsole(output io.Writer, step Step) (int, error) {
	if _, _, err := pty.Size(int(os.Stdout.Fd())); err != nil {
		return 0, ErrNoConsole
	}
	restore, err := pty.Raw(int(os.Stdin.Fd()), false)
	if err != nil {
		return 0, ErrNoConsole
	}
	defer restore()
	keys := &terminalKeys{input: os.Stdin}
	menu := Menu{Output: output, Width: ConsoleWidth, NoColor: NoColor(), ReadKey: keys.next}
	return menu.Ask(step)
}

// terminalKeys reads the keys a terminal in raw mode sends as bytes.
type terminalKeys struct {
	input   io.Reader
	pending []Key
}

// next is the next key the terminal sends that a menu acts on.
func (k *terminalKeys) next() (Key, error) {
	chunk := make([]byte, 256)
	for len(k.pending) == 0 {
		n, err := k.input.Read(chunk)
		k.pending = append(k.pending, parseKeys(chunk[:n])...)
		if err != nil && len(k.pending) == 0 {
			return 0, err
		}
	}
	key := k.pending[0]
	k.pending = k.pending[1:]
	return key, nil
}

// parseKeys is the keys a menu acts on in what one read of a terminal gave.
// A terminal sends a key's whole sequence in one write, so an arrow, ESC [ A
// or ESC O A, comes whole, and an Escape is an ESC that no [ or O follows in
// the same read: no timing tells them apart, as Windows' key events need
// none. Enter is a carriage return or a newline, Ctrl-C reads as Escape, as
// on Windows, and a letter reads as its lower case.
func parseKeys(chunk []byte) []Key {
	arrows := map[byte]Key{'A': KeyUp, 'B': KeyDown, 'C': KeyRight, 'D': KeyLeft}
	var keys []Key
	for i := 0; i < len(chunk); {
		switch b := chunk[i]; {
		case b == 0x1b && i+1 < len(chunk) && (chunk[i+1] == '[' || chunk[i+1] == 'O'):
			end := i + 2
			for end < len(chunk) && (chunk[end] >= '0' && chunk[end] <= '9' || chunk[end] == ';') {
				end++
			}
			if end < len(chunk) {
				if arrow, isArrow := arrows[chunk[end]]; isArrow {
					keys = append(keys, arrow)
				}
			}
			i = end + 1
		case b == 0x1b || b == 0x03:
			keys = append(keys, KeyEscape)
			i++
		case b == '\r' || b == '\n':
			keys = append(keys, KeyEnter)
			i++
		default:
			letter, size := utf8.DecodeRune(chunk[i:])
			if letter = unicode.ToLower(letter); unicode.IsLetter(letter) {
				keys = append(keys, Key(letter))
			}
			i += size
		}
	}
	return keys
}
