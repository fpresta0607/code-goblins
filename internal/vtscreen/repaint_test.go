package vtscreen

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// outputs are what programs draw, each with what the repaint must carry
// over: styles, wide characters and marks, scrolling, the alternate screen,
// modes, a scrolling region in origin mode, line drawing, a saved cursor and
// a wrap pending.
var outputs = map[string]string{
	"styled lines that scroll": strings.Repeat("\x1b[1;31mred\x1b[0m 日本 é \x1b[4:3;58;5;9;38;2;1;2;3;48;5;200mfancy\x1b[m\r\n", 9) + "\x1b[7mlast\x1b[27m",
	"a full-screen program":    "shell $ codex\r\n\x1b[?1049h\x1b[?1h\x1b=\x1b[?2004h\x1b[?1004h\x1b[?1003;1006h\x1b[?25l\x1b[H\x1b[2J\x1b[44m box \x1b[0m\x1b[3;2H\x1b[92m›\x1b[39m type here\x1b[2 q",
	"a region in origin mode":  "top\r\n\x1b[2;5r\x1b[?6h\x1b[1;1Hin the region\x1b[3;4H\x1b[3m*\x1b[23m\x1b[4h\x1b[20h",
	"a wrap pending":           "first\r\n\x1b[6;1H" + strings.Repeat("w", 18) + "日",
	"line drawing and a save":  "\x1b[2;3H\x1b[1m\x1b7\x1b[22m\r\nnext\x1b(0lqqk\x0e\x1b)0",
	"modes reset":              "\x1b[?7l" + strings.Repeat("n", 25) + "\x1b[?2004h\x1b[?2004l\x1b[?25l",
	"synchronized output":      "\x1b[?2026hbefore\x1b[?2026l\x1b[?2026hduring",
}

// A viewer that replayed only the end of a terminal's output, as one that
// reconnects after the host trimmed its history does, shows the screen the
// whole output drew once it is passed the repaint, whatever the part it
// replayed left it showing.
func TestARepaintRestoresTheScreenOnAViewerThatReplayedOnlyTheEnd(t *testing.T) {
	for name, output := range outputs {
		for _, cut := range lineStarts(output) {
			t.Run(fmt.Sprintf("%s from byte %d", name, cut), func(t *testing.T) {
				// Arrange
				model, viewer := newScreen(t, 20, 6), newScreen(t, 20, 6)
				model.Write([]byte(output))
				viewer.Write([]byte(output[cut:]))

				// Act
				viewer.Write(model.Repaint())

				// Assert
				sameScreen(t, model, viewer)
			})
		}
	}
}

// lineStarts are the places a replay can start: the output's start and
// each line's.
func lineStarts(output string) []int {
	starts := []int{0}
	for i, b := range []byte(output) {
		if b == '\n' && i+1 < len(output) {
			starts = append(starts, i+1)
		}
	}
	return starts
}

// A repaint passes nothing a terminal would answer, so a viewer that
// answers what it is shown types nothing into the program.
func TestARepaintAsksNothing(t *testing.T) {
	for name, output := range outputs {
		t.Run(name, func(t *testing.T) {
			// Arrange
			model := newScreen(t, 20, 6)
			model.Write([]byte(output))

			// Act
			forward, answers := newScreen(t, 20, 6).Write(model.Repaint())

			// Assert
			if len(answers) > 0 || !bytes.Equal(forward, model.Repaint()) {
				t.Fatalf("a viewer shown the repaint answered %q and passed on %q, want no answers and all of it", answers, forward)
			}
		})
	}
}

// After a resize, every viewer shows the screen as the host keeps it,
// however each resized its own copy, so the cursor the host reports is
// where every viewer shows it.
func TestAResizeRepaintsEveryViewerToTheHostsScreen(t *testing.T) {
	// Arrange
	model, viewer := newScreen(t, 20, 6), newScreen(t, 20, 6)
	output := []byte(outputs["styled lines that scroll"])
	model.Write(output)
	viewer.Write(output)
	// A viewer that reflowed its lines differently on the narrower screen.
	viewer.Resize(12, 4)
	viewer.Write([]byte("\x1b[H\x1b[2Jreflowed\r\nelsewhere"))

	// Act
	repaint, err := model.Resize(12, 4)
	viewer.Write(repaint)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	sameScreen(t, model, viewer)
}

// sameScreen fails the test unless got shows what want does: the cells
// drawn, the cursor, the style, the modes, the margins, the character sets
// and the saved cursors a viewer can restore.
func sameScreen(t *testing.T, want, got *Screen) {
	t.Helper()
	if want.isAlt != got.isAlt {
		t.Fatalf("alternate screen %v, want %v", got.isAlt, want.isAlt)
	}
	sameCells(t, "main", want.main, got.main)
	if want.isAlt {
		sameCells(t, "alternate", want.alt, got.alt)
		sameSaved(t, "main", want.saved[0], got.saved[0])
	}
	sameSaved(t, "active", want.saved[want.screenIndex()], got.saved[got.screenIndex()])
	type state struct {
		cursor                                            cursor
		pen                                               style
		top, bottom                                       int
		origin, autowrap, insert, newline, hidden, keypad bool
		charsets                                          [4]bool
		shift, cursorStyle                                int
	}
	view := func(s *Screen) state {
		return state{s.cursor, s.pen, s.top, s.bottom, s.isOriginMode, s.isAutowrap, s.isInsert, s.isNewlineMode, s.isCursorHidden, s.isKeypadMode, s.charsets, s.shift, s.cursorStyle}
	}
	if view(want) != view(got) {
		t.Fatalf("state %+v, want %+v", view(got), view(want))
	}
	for mode := range unionOf(want.modes, got.modes) {
		if want.modes[mode] != got.modes[mode] {
			t.Errorf("mode %d set %v, want %v", mode, got.modes[mode], want.modes[mode])
		}
	}
}

func unionOf(a, b map[int]bool) map[int]bool {
	union := map[int]bool{}
	for mode := range a {
		union[mode] = true
	}
	for mode := range b {
		union[mode] = true
	}
	return union
}

func sameCells(t *testing.T, name string, want, got *grid) {
	t.Helper()
	shown := func(c cell) cell {
		if c.char == 0 && c.width == 1 {
			c.char = ' '
		}
		return c
	}
	for y := range want.lines {
		for x := range want.lines[y] {
			if shown(want.lines[y][x]) != shown(got.lines[y][x]) {
				t.Fatalf("%s screen cell %d,%d is %+v, want %+v\nrows %q\nwant %q", name, y, x, got.lines[y][x], want.lines[y][x], rowsOf(got), rowsOf(want))
			}
		}
	}
}

func sameSaved(t *testing.T, name string, want, got savedCursor) {
	t.Helper()
	if want.cursor != got.cursor || want.style != got.style {
		t.Fatalf("%s saved cursor %+v in %+v, want %+v in %+v", name, got.cursor, got.style, want.cursor, want.style)
	}
}

func rowsOf(g *grid) []string {
	var rows []string
	for _, line := range g.lines {
		var row strings.Builder
		for _, c := range line {
			row.Write(c.appendShown(nil))
		}
		rows = append(rows, row.String())
	}
	return rows
}
