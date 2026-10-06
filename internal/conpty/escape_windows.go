package conpty

import (
	"fmt"
	"regexp"
	"strings"
)

// Once conhost has read one key event written as Windows sends it, in
// win32-input-mode (ESC [ Vk;Sc;Uc;Kd;Cs;Rc _), as cfo attach writes every key
// it is given, it reads an input that ends in Escape, or in Escape and a
// character that starts a longer sequence, as the start of a sequence still
// arriving: it holds it until the next key, then reads the two together, so
// an Escape typed alone reaches the program only with the next key, as Alt
// and that key. The console holds such an ending back instead, for the next
// write to take on when it comes at once, as the rest of a key event split
// across two writes does, and otherwise writes it as the key events it
// stands for, which conhost delivers at once.

// windowsKeyEvent is a key event written as Windows sends it.
var windowsKeyEvent = regexp.MustCompile(`\x1b\[[0-9;]*_`)

// sequenceStarts are the characters that, after Escape, start a sequence
// longer than the two: CSI, SS3, DCS, OSC, SOS, PM and APC.
const sequenceStarts = "[OP]X^_"

// escapeKey is Escape pressed and released, as Windows sends it.
const escapeKey = "\x1b[27;1;27;1;0;1_\x1b[27;1;27;0;0;1_"

// leftAltPressed is LEFT_ALT_PRESSED, a key event's control key state with
// the left Alt key down.
const leftAltPressed = 2

// heldEnding is how many bytes at the end of p conhost would hold once it
// has read a Windows key event: one for a lone Escape, two for Escape and a
// character that starts a longer sequence, and otherwise none.
func heldEnding(p []byte) int {
	switch {
	case len(p) >= 1 && p[len(p)-1] == 0x1b:
		return 1
	case len(p) >= 2 && p[len(p)-2] == 0x1b && strings.IndexByte(sequenceStarts, p[len(p)-1]) >= 0:
		return 2
	}
	return 0
}

// asKeyEvents is an ending heldEnding found written as the Windows key events
// it stands for: a lone Escape as Escape, and Escape and a character as Alt
// and that character.
func asKeyEvents(ending []byte) []byte {
	if len(ending) == 1 {
		return []byte(escapeKey)
	}
	character := ending[1]
	return fmt.Appendf(nil, "\x1b[0;0;%d;1;%d;1_\x1b[0;0;%d;0;%d;1_", character, leftAltPressed, character, leftAltPressed)
}
