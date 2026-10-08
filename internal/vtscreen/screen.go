// Package vtscreen keeps a terminal's screen from the output its program
// writes, so the host of a native terminal can be that terminal's one
// terminal of record: it answers the queries a terminal answers, such as
// where the cursor is, keeps every query from the viewers that replay and
// follow the output, and repaints them from what it holds.
//
// No small Go terminal state library fits: midterm and vt10x draw every
// character one cell wide, so the cursor they report is wrong on any line
// with a wide character, and charmbracelet/x/vt is published only as
// pseudo-versions and brings about ten modules. The parsing is go-vte's, a
// port of the parser Alacritty uses, which follows the DEC state machine.
// The screen's state is this package's own.
package vtscreen

import (
	"fmt"

	"github.com/danielgatis/go-vte"
)

// minSize and maxSize bound a screen's width and height, as internal/conpty
// bounds a pseudo console's.
const minSize, maxSize = 2, 1000

// unitLimit bounds how much of one escape sequence a screen holds back from
// its viewers until it ends, so a sequence that never ends, or a long one
// such as an image, is passed on as it comes once it outgrows it.
const unitLimit = 1 << 20

// cursor is where the next character goes. At the last column, a character
// written leaves the cursor there with a wrap pending: the next character
// goes to the start of the next row.
type cursor struct {
	x, y          int
	isWrapPending bool
}

// savedCursor is what DECSC saves and DECRC restores.
type savedCursor struct {
	cursor
	style        style
	isOriginMode bool
	charsets     [4]bool
	shift        int
}

// Screen is a terminal's screen, main and alternate, its cursor, and the
// modes its program set. It is not safe for concurrent use.
type Screen struct {
	parser     *vte.Parser
	cols, rows int
	main, alt  *grid
	isAlt      bool
	cursor     cursor
	pen        style
	// saved holds what DECSC saved on the main screen and on the
	// alternate one.
	saved [2]savedCursor
	// top and bottom are the rows the scrolling region spans.
	top, bottom    int
	isOriginMode   bool
	isAutowrap     bool
	isInsert       bool
	isNewlineMode  bool
	isCursorHidden bool
	isKeypadMode   bool
	tabs           []bool
	// charsets holds whether each of G0 to G3 draws DEC line drawing, and
	// shift which of G0 and G1 draws now.
	charsets [4]bool
	shift    int
	// modes are the DEC private modes the program set or reset that the
	// screen passes on as they are, in the order first seen, for a repaint
	// to set again.
	modes       map[int]bool
	modeOrder   []int
	cursorStyle int
	// lastPrinted is the character REP repeats.
	lastPrinted rune

	// unit is the sequence, or the bytes of one character, the parser is
	// in the middle of, held back from viewers until it ends. isDropped says
	// it is a query the screen keeps from them, and isStreaming that it
	// outgrew unitLimit and is passed on as it comes.
	unit        []byte
	isDropped   bool
	isStreaming bool
	// isRepaintDue says a resize came while a sequence was being passed on,
	// so the repaint waits for its end.
	isRepaintDue bool
	// forward and answers are what the Write in progress passes on and
	// answers.
	forward, answers []byte
}

// New is the screen of a terminal of cols by rows cells, as a terminal
// starts.
func New(cols, rows int) (*Screen, error) {
	if err := checkSize(cols, rows); err != nil {
		return nil, err
	}
	s := &Screen{cols: cols, rows: rows}
	s.parser = vte.NewParser(s)
	s.reset()
	return s, nil
}

func checkSize(cols, rows int) error {
	if cols < minSize || rows < minSize || cols > maxSize || rows > maxSize {
		return fmt.Errorf("vtscreen: size %dx%d is outside %d to %d cells", cols, rows, minSize, maxSize)
	}
	return nil
}

// reset is a terminal's state as it starts, at its current size.
func (s *Screen) reset() {
	s.main, s.alt = newGrid(s.cols, s.rows), newGrid(s.cols, s.rows)
	s.isAlt = false
	s.cursor, s.pen = cursor{}, style{}
	s.saved = [2]savedCursor{}
	s.top, s.bottom = 0, s.rows-1
	s.isOriginMode, s.isAutowrap, s.isInsert, s.isNewlineMode = false, true, false, false
	s.isCursorHidden, s.isKeypadMode = false, false
	s.tabs = defaultTabs(s.cols, nil)
	s.charsets, s.shift = [4]bool{}, 0
	s.modes, s.modeOrder = map[int]bool{}, nil
	s.cursorStyle = 0
	s.lastPrinted = 0
}

// defaultTabs is a tab stop every 8 columns across cols, keeping the stops
// of kept where it reaches.
func defaultTabs(cols int, kept []bool) []bool {
	tabs := make([]bool, cols)
	for x := range tabs {
		tabs[x] = x%8 == 0
	}
	copy(tabs, kept)
	return tabs
}

// Write takes output the terminal's program wrote and returns what the
// terminal's viewers are to be passed, which is output without the queries
// the screen keeps from them, and what the screen answers the program. A
// sequence output ends inside is held back until a later Write ends it, so
// what is passed on always ends between sequences and characters.
func (s *Screen) Write(output []byte) (forward, answers []byte) {
	for _, b := range output {
		if s.isStreaming {
			s.forward = append(s.forward, b)
			// go-vte keeps every byte of an SOS, PM or APC string, which the
			// screen never reads, so past the limit only a byte that can end
			// one reaches it.
			if s.parser.State() != vte.SosPmApcStringState || b == 0x07 || b == 0x18 || b == 0x1a || b == 0x1b {
				s.parser.Advance(b)
			}
			if s.parser.State() == vte.GroundState {
				s.endUnit()
			}
			continue
		}
		s.unit = append(s.unit, b)
		s.parser.Advance(b)
		switch {
		case s.parser.State() == vte.GroundState:
			if !s.isDropped {
				s.forward = append(s.forward, s.unit...)
			}
			s.unit = s.unit[:0]
			s.endUnit()
		case len(s.unit) > unitLimit:
			s.forward = append(s.forward, s.unit...)
			s.unit = s.unit[:0]
			s.isStreaming = true
		}
	}
	forward, answers = s.forward, s.answers
	s.forward, s.answers = nil, nil
	return forward, answers
}

// endUnit follows the end of a sequence or a character: the next starts
// afresh, and a resize's repaint that waited for this end is passed on.
func (s *Screen) endUnit() {
	s.isDropped, s.isStreaming = false, false
	if s.isRepaintDue {
		s.isRepaintDue = false
		s.forward = append(s.forward, s.repaint(false)...)
	}
}

// Resize gives the screen cols by rows cells, as a terminal resizes, and
// returns the repaint its viewers are to be passed now: the whole screen
// drawn again, even at the size it had, as the system conhost repaints on
// every resize, since OpenConsole repaints on none while each viewer resizes
// its own copy of the screen its own way, and a viewer that sends its size
// as it connects counts on the repaint to show the screen whole. Viewers
// that followed the output keep the modes the program set, so this repaint
// sets none of them again: xterm.js reports the focus each time it is asked
// to. During a sequence passed on as it comes, the repaint follows that
// sequence's end, in a later Write.
func (s *Screen) Resize(cols, rows int) ([]byte, error) {
	if err := checkSize(cols, rows); err != nil {
		return nil, err
	}
	if cols != s.cols || rows != s.rows {
		s.resize(cols, rows)
	}
	if s.isStreaming {
		s.isRepaintDue = true
		return nil, nil
	}
	return s.repaint(false), nil
}

// resize gives the screen a new size as xterm.js resizes its own: the main
// screen's lines wrapped anew at a new width, the alternate screen's rows
// cut or filled at their ends, and rows lost or gained around each screen's
// cursor.
func (s *Screen) resize(cols, rows int) {
	// The main screen's cursor is the live one, or while the alternate
	// screen is in use the one entering it saved.
	mainCursor, altCursor := &s.cursor, &s.saved[1].cursor
	if s.isAlt {
		mainCursor, altCursor = &s.saved[0].cursor, &s.cursor
	}
	if cols != s.cols {
		mainCursor.y = s.main.reflow(cols, mainCursor.y)
	}
	mainCursor.y = s.main.fit(cols, rows, mainCursor.y)
	altCursor.y = s.alt.fit(cols, rows, altCursor.y)
	// A cursor with a wrap pending sits past the last column, as xterm.js
	// keeps it, which a wider screen makes the column after it.
	for _, at := range []*cursor{&s.cursor, &s.saved[0].cursor, &s.saved[1].cursor} {
		if at.isWrapPending && cols > s.cols {
			at.x++
		}
		at.x, at.y = min(at.x, cols-1), min(at.y, rows-1)
		at.isWrapPending = false
	}
	s.cols, s.rows = cols, rows
	s.top, s.bottom = 0, rows-1
	s.tabs = defaultTabs(cols, s.tabs)
}

// Rows is the text of each row of the screen the program draws on now, a
// blank cell as a space.
func (s *Screen) Rows() []string {
	rows := make([]string, 0, s.rows)
	for _, r := range s.screen().rows {
		var text []byte
		for _, c := range r.cells {
			if c.width != 0 {
				text = c.appendShown(text)
			}
		}
		rows = append(rows, string(text))
	}
	return rows
}

// screen is the grid the program draws on now.
func (s *Screen) screen() *grid {
	if s.isAlt {
		return s.alt
	}
	return s.main
}

// cursorReport is the cursor position report, CPR, a terminal answers
// DSR 6 with: the cursor's row and column from 1, the row counted from the
// scrolling region's top in origin mode.
func (s *Screen) cursorReport(private bool) []byte {
	row := s.cursor.y + 1
	if s.isOriginMode {
		row -= s.top
	}
	marker := ""
	if private {
		marker = "?"
	}
	return fmt.Appendf(nil, "\x1b[%s%d;%dR", marker, row, s.cursor.x+1)
}
