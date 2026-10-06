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
// and that key. Such an ending is written as the key events it stands for
// instead, which conhost delivers at once.

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

// endingAsKeyEvents returns p with an ending conhost would hold, once it has
// read a Windows key event, written as the key events it stands for: a lone
// Escape as Escape, and Escape and a character that starts a longer sequence
// as Alt and that character.
func endingAsKeyEvents(p []byte) []byte {
	switch {
	case len(p) >= 1 && p[len(p)-1] == 0x1b:
		return append(p[:len(p)-1:len(p)-1], escapeKey...)
	case len(p) >= 2 && p[len(p)-2] == 0x1b && strings.IndexByte(sequenceStarts, p[len(p)-1]) >= 0:
		character := p[len(p)-1]
		pressed := fmt.Sprintf("\x1b[0;0;%d;1;%d;1_\x1b[0;0;%d;0;%d;1_", character, leftAltPressed, character, leftAltPressed)
		return append(p[:len(p)-2:len(p)-2], pressed...)
	}
	return p
}
