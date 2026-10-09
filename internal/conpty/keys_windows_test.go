package conpty

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The control key states of a key record that the key table reads beside
// Alt: RIGHT_CTRL_PRESSED or LEFT_CTRL_PRESSED, and SHIFT_PRESSED.
const (
	ctrlPressed  = 0x4 | 0x8
	shiftPressed = 0x10
)

// boardKey is one key as the board's terminal sends it, the bytes xterm
// writes for it, and the key record a program reads for those bytes: its
// virtual key, its character and whether Alt, Ctrl and Shift are down.
type boardKey struct {
	name, typed      string
	key              uint16
	character        rune
	alt, ctrl, shift bool
}

func (k boardKey) record() string {
	return fmt.Sprintf("key %d character %04x alt %t ctrl %t shift %t", k.key, k.character, k.alt, k.ctrl, k.shift)
}

// boardKeys is the key table of frontend/tests/terminal-keys.spec.ts from the
// console's side: what each key's bytes become for a program that reads key
// records.
func boardKeys() []boardKey {
	keys := []boardKey{
		{name: "Enter", typed: "\r", key: 13, character: '\r'},
		{name: "Escape", typed: "\x1b", key: 27, character: 0x1b},
		{name: "Tab", typed: "\t", key: 9, character: '\t'},
		{name: "Shift+Tab", typed: "\x1b[Z", key: 9, character: '\t', shift: true},
		{name: "Space", typed: " ", key: 32, character: ' '},
		{name: "Backspace", typed: "\x7f", key: 8, character: '\b'},
		{name: "Delete", typed: "\x1b[3~", key: 46},
		{name: "Insert", typed: "\x1b[2~", key: 45},
		{name: "ArrowUp", typed: "\x1b[A", key: 38},
		{name: "ArrowDown", typed: "\x1b[B", key: 40},
		{name: "ArrowRight", typed: "\x1b[C", key: 39},
		{name: "ArrowLeft", typed: "\x1b[D", key: 37},
		{name: "Home", typed: "\x1b[H", key: 36},
		{name: "End", typed: "\x1b[F", key: 35},
		{name: "PageUp", typed: "\x1b[5~", key: 33},
		{name: "PageDown", typed: "\x1b[6~", key: 34},
		{name: "r", typed: "r", key: 'R', character: 'r'},
		{name: "Shift+r", typed: "R", key: 'R', character: 'R', shift: true},
		{name: "1", typed: "1", key: '1', character: '1'},
		{name: "/", typed: "/", key: 191, character: '/'},
		{name: "?", typed: "?", key: 191, character: '?', shift: true},
		{name: "Alt+Enter", typed: "\x1b\r", key: 13, character: '\r', alt: true},
		{name: "Alt+Backspace", typed: "\x1b\x7f", key: 8, character: '\b', alt: true},
		{name: "Control+Backspace", typed: "\b", key: 8, character: 0x7f, ctrl: true},
		{name: "Control+Delete", typed: "\x1b[3;5~", key: 46, ctrl: true},
		{name: "Shift+ArrowUp", typed: "\x1b[1;2A", key: 38, shift: true},
		{name: "Alt+ArrowLeft", typed: "\x1b[1;3D", key: 37, alt: true},
		{name: "Control+ArrowRight", typed: "\x1b[1;5C", key: 39, ctrl: true},
		{name: "Shift+Home", typed: "\x1b[1;2H", key: 36, shift: true},
		{name: "Control+Home", typed: "\x1b[1;5H", key: 36, ctrl: true},
		{name: "Control+End", typed: "\x1b[1;5F", key: 35, ctrl: true},
		{name: "Control+PageUp", typed: "\x1b[5;5~", key: 33, ctrl: true},
		{name: "Control+PageDown", typed: "\x1b[6;5~", key: 34, ctrl: true},
		{name: "F1", typed: "\x1bOP", key: 112},
		{name: "F2", typed: "\x1bOQ", key: 113},
		{name: "F3", typed: "\x1bOR", key: 114},
		{name: "F4", typed: "\x1bOS", key: 115},
		{name: "F5", typed: "\x1b[15~", key: 116},
		{name: "F6", typed: "\x1b[17~", key: 117},
		{name: "F7", typed: "\x1b[18~", key: 118},
		{name: "F8", typed: "\x1b[19~", key: 119},
		{name: "F9", typed: "\x1b[20~", key: 120},
		{name: "F10", typed: "\x1b[21~", key: 121},
		{name: "F11", typed: "\x1b[23~", key: 122},
		{name: "F12", typed: "\x1b[24~", key: 123},
		// Bytes cannot tell these keys from another, as in any terminal that
		// sends bytes: NUL is read as Ctrl+Shift+2, a line feed as
		// Ctrl+Enter, and Ctrl+H, Ctrl+I and Ctrl+M are the bytes of
		// Ctrl+Backspace, Tab and Enter.
		{name: "Control+Space", typed: "\x00", key: '2', ctrl: true, shift: true},
		{name: "Control+j", typed: "\n", key: 13, character: '\n', ctrl: true},
		{name: "Control+h", typed: "\b", key: 8, character: 0x7f, ctrl: true},
		{name: "Control+i", typed: "\t", key: 9, character: '\t'},
		{name: "Control+m", typed: "\r", key: 13, character: '\r'},
		{name: "Control+Backslash", typed: "\x1c", key: 220, character: 0x1c, ctrl: true},
		{name: "Control+BracketRight", typed: "\x1d", key: 221, character: 0x1d, ctrl: true},
		{name: "Control+Shift+Minus", typed: "\x1f", key: 189, character: 0x1f, ctrl: true, shift: true},
		// Codex's composer takes a line break as Ctrl+J, which the board
		// sends it as the key event Windows writes.
		{name: "Shift+Enter for Codex", typed: "\x1b[74;36;10;1;8;1_\x1b[74;36;10;0;8;1_", key: 'J', character: '\n', ctrl: true},
	}
	for letter := 'a'; letter <= 'z'; letter++ {
		upper := uint16(letter - 'a' + 'A')
		keys = append(keys, boardKey{name: "Alt+" + string(letter), typed: "\x1b" + string(letter), key: upper, character: letter, alt: true})
		// Ctrl+V is the board's paste, and Ctrl+H, Ctrl+I, Ctrl+J and Ctrl+M
		// are above.
		if strings.ContainsRune("vhijm", letter) {
			continue
		}
		keys = append(keys, boardKey{name: "Control+" + string(letter), typed: string(letter - 'a' + 1), key: upper, character: letter - 'a' + 1, ctrl: true})
	}
	return keys
}

// A program that reads its console's input a key record at a time, as Codex
// does through crossterm, never sees the bytes the board sends: the pseudo
// console makes key records of them. Each key of the board's table reaches
// such a program as the key Windows names for it, with its character and its
// Alt, Ctrl and Shift, so a screen that answers to a key in a plain terminal
// answers to it in a board terminal.
func TestEachKeyTheBoardSendsReachesAProgramThatReadsKeyRecordsAsThatKey(t *testing.T) {
	progressPath := filepath.Join(t.TempDir(), "keys.log")
	console, s := startChild(t, Spec{
		Args: []string{os.Args[0], "-test.run=^TestKeyRecordChild$", "--", "key-record-child", progressPath},
		Env:  os.Environ(),
		Cols: 80, Rows: 25,
	})
	s.waitFor(t, "key-records-ready")
	// The records of the keys themselves: Shift, Ctrl and Alt going down
	// before a key are records too, and each key's own record carries them.
	read := func() []string {
		progress, _ := os.ReadFile(progressPath)
		var records []string
		for _, line := range strings.Split(strings.TrimSpace(string(progress)), "\n") {
			if line != "" && !strings.HasPrefix(line, "key 16 ") && !strings.HasPrefix(line, "key 17 ") && !strings.HasPrefix(line, "key 18 ") {
				records = append(records, line)
			}
		}
		return records
	}

	for _, key := range boardKeys() {
		// Arrange
		before := len(read())

		// Act
		if _, err := console.Write([]byte(key.typed)); err != nil {
			t.Fatalf("%s: %v", key.name, err)
		}

		// Assert
		var records []string
		for deadline := time.Now().Add(2 * time.Second); len(records) == 0 && time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
			records = read()[before:]
		}
		// A key that became two records would show its second one here.
		time.Sleep(20 * time.Millisecond)
		records = read()[before:]
		if got := strings.Join(records, ", "); got != key.record() {
			t.Errorf("%s typed as %q reached the program as %q, want %q", key.name, key.typed, got, key.record())
		}
	}
}
