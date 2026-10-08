package vtscreen

import (
	"fmt"
	"strings"
)

// Repaint is what draws the screen again on a viewer in any state, from a
// terminal that just started to one that replayed part of the output: the
// main screen, the alternate one over it when it is in use, the modes and
// style the program set, the saved cursor and the cursor. It writes nothing
// that a terminal answers. Between a sequence's start and its end, while it
// is passed on as it comes, a repaint would land inside it, so it is empty.
func (s *Screen) Repaint() []byte {
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
	out = append(out, s.modeSettings()...)
	// A program in the middle of synchronized output ends it itself.
	if !s.modes[2026] {
		out = append(out, "\x1b[?2026l"...)
	}
	return out
}

// paint draws every row of g from the top left, each row's cells in their
// styles and its trailing blanks erased.
func (s *Screen) paint(out []byte, g *grid) []byte {
	for y, line := range g.lines {
		out = fmt.Appendf(out, "\x1b[%d;1H", y+1)
		end := len(line)
		for end > 0 && line[end-1] == blank(style{}) {
			end--
		}
		var current style
		out = current.appendSGR(out)
		for _, c := range line[:end] {
			if c.width == 0 {
				continue
			}
			if c.style != current {
				current = c.style
				out = current.appendSGR(out)
			}
			out = c.appendShown(out)
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
	g := s.screen()
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
	if x > 0 && g.lines[s.cursor.y][x].width == 0 {
		x--
	}
	last := g.lines[s.cursor.y][x]
	out = fmt.Appendf(out, "\x1b[%d;%dH", at.y+1, x+1)
	out = last.style.appendSGR(out)
	out = last.appendShown(out)
	return s.pen.appendSGR(out)
}

// modeSettings sets every mode the program changed from how a terminal
// starts.
func (s *Screen) modeSettings() string {
	var out strings.Builder
	set := func(mode int, isSet bool) {
		letter := "l"
		if isSet {
			letter = "h"
		}
		fmt.Fprintf(&out, "\x1b[?%d%s", mode, letter)
	}
	for _, mode := range s.modeOrder {
		if mode != 2026 {
			set(mode, s.modes[mode])
		}
	}
	if !s.isAutowrap {
		set(7, false)
	}
	if s.isInsert {
		out.WriteString("\x1b[4h")
	}
	if s.isNewlineMode {
		out.WriteString("\x1b[20h")
	}
	if s.isKeypadMode {
		out.WriteString("\x1b=")
	}
	if s.cursorStyle != 0 {
		fmt.Fprintf(&out, "\x1b[%d q", s.cursorStyle)
	}
	if !s.isCursorHidden {
		set(25, true)
	}
	return out.String()
}
