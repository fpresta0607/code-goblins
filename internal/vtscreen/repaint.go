package vtscreen

import (
	"fmt"
	"strings"
)

// Repaint is what draws the screen again on a viewer in any state, from a
// terminal that just started to one that replayed part of the output: the
// main screen, the alternate one over it when it is in use, the modes and
// style the program set, the saved cursor and the cursor. It writes nothing
// that a terminal answers, though xterm.js reports the focus when it is
// asked for focus reports again. Between a sequence's start and its end,
// while it is passed on as it comes, a repaint would land inside it, so it
// is empty.
func (s *Screen) Repaint() []byte {
	return s.repaint(true)
}

// repaint draws the screen again, and with isWithModes the modes the
// program set too, which a viewer that followed the output already has.
func (s *Screen) repaint(isWithModes bool) []byte {
	if s.isStreaming {
		return nil
	}
	// The viewer draws the repaint at once, as synchronized output. It
	// leaves the alternate screen, which restores a cursor this repaint
	// sets again, and starts from a cursor, margins, characters and keypad
	// that place each row where it is asked for.
	out := []byte("\x1b[?2026h\x1b[?25l\x1b[?1049l\x1b[?6l\x1b[?7h\x1b[4l\x1b[r\x1b(B\x0f\x1b>")
	out = s.paint(out, s.main)
	if s.isAlt {
		// Entering the alternate screen saves the cursor that leaving it
		// restores: the one the program saved as it entered.
		out = s.placeSaved(out, s.saved[0])
		out = append(out, "\x1b[?1049h"...)
		out = s.paint(out, s.alt)
	}
	out = s.placeSaved(out, s.saved[s.screenIndex()])
	out = append(out, "\x1b7"...)
	for i, isLineDrawing := range s.charsets {
		if isLineDrawing {
			out = append(out, "\x1b"+string("()*+"[i])+"0"...)
		}
	}
	if s.shift == 1 {
		out = append(out, '\x0e')
	}
	if s.top != 0 || s.bottom != s.rows-1 {
		out = fmt.Appendf(out, "\x1b[%d;%dr", s.top+1, s.bottom+1)
	}
	out = s.placeCursor(out)
	out = append(out, s.restored()...)
	if isWithModes {
		out = append(out, s.modeSettings()...)
	}
	// A program in the middle of synchronized output ends it itself.
	if !s.modes[2026] {
		out = append(out, "\x1b[?2026l"...)
	}
	return out
}

// paint draws every row of g from the top left, each row's cells in their
// styles and its trailing blanks erased. A row the one above continued is
// reached by writing on past that row's end, so the viewer marks it as
// continued too and joins the two when it reflows them.
func (s *Screen) paint(out []byte, g *grid) []byte {
	for y, r := range g.rows {
		line := r.cells
		end := len(line)
		if isContinued := y+1 < len(g.rows) && g.rows[y+1].isWrapped; !isContinued {
			for end > 0 && line[end-1] == blank(style{}) {
				end--
			}
		}
		if y == 0 || !r.isWrapped {
			out = fmt.Appendf(out, "\x1b[%d;1H", y+1)
		}
		var current style
		out = current.appendSGR(out)
		for x, c := range line[:end] {
			if c.width == 0 {
				continue
			}
			if c.style != current {
				current = c.style
				out = current.appendSGR(out)
			}
			out = c.appendShown(out)
			// A viewer may draw a wide character one cell wide, so the next
			// cell is placed by its column.
			if c.width == 2 && x+2 < len(line) {
				out = fmt.Appendf(out, "\x1b[%dG", x+3)
			}
		}
		if end < len(line) {
			out = append(out, "\x1b[0m\x1b[K"...)
		}
	}
	return out
}

// placeSaved moves a viewer's cursor and style to what saved holds, for the
// viewer to save with them.
func (s *Screen) placeSaved(out []byte, saved savedCursor) []byte {
	out = fmt.Appendf(out, "\x1b[%d;%dH", saved.y+1, saved.x+1)
	return saved.style.appendSGR(out)
}

// placeCursor moves a viewer's cursor where the screen's is, its wrap
// pending too, and sets the style the program draws in.
func (s *Screen) placeCursor(out []byte) []byte {
	line := s.screen().rows[s.cursor.y].cells
	at := s.cursor
	if s.isOriginMode {
		out = append(out, "\x1b[?6h"...)
		at.y -= s.top
	}
	if !s.cursor.isWrapPending {
		out = fmt.Appendf(out, "\x1b[%d;%dH", at.y+1, at.x+1)
		return s.pen.appendSGR(out)
	}
	// A wrap is pending once the last cell is written, so the last cell is
	// written again from the column it starts in.
	x := s.cursor.x
	if x > 0 && line[x].width == 0 {
		x--
	}
	last := line[x]
	out = fmt.Appendf(out, "\x1b[%d;%dH", at.y+1, x+1)
	out = last.style.appendSGR(out)
	out = last.appendShown(out)
	return s.pen.appendSGR(out)
}

// restored sets again what a repaint's start changed of the program's
// settings: autowrap, insert mode, the keypad and the cursor's showing.
func (s *Screen) restored() string {
	var out strings.Builder
	if !s.isAutowrap {
		out.WriteString("\x1b[?7l")
	}
	if s.isInsert {
		out.WriteString("\x1b[4h")
	}
	if s.isKeypadMode {
		out.WriteString("\x1b=")
	}
	if !s.isCursorHidden {
		out.WriteString("\x1b[?25h")
	}
	return out.String()
}

// modeSettings sets every other mode the program changed from how a
// terminal starts, for a viewer that missed where it changed them.
func (s *Screen) modeSettings() string {
	var out strings.Builder
	for _, mode := range s.modeOrder {
		if mode == 2026 {
			continue
		}
		letter := "l"
		if s.modes[mode] {
			letter = "h"
		}
		fmt.Fprintf(&out, "\x1b[?%d%s", mode, letter)
	}
	if s.isNewlineMode {
		out.WriteString("\x1b[20h")
	}
	if s.cursorStyle != 0 {
		fmt.Fprintf(&out, "\x1b[%d q", s.cursorStyle)
	}
	return out.String()
}
