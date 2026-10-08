package vtscreen

import (
	"bytes"
	"strconv"
	"unicode/utf8"

	"github.com/danielgatis/go-vte"
)

// The answers the screen gives, as xterm.js gives them: a terminal in good
// order, and the device attributes the console's own query gets
// (internal/conpty), a VT100 with advanced video.
const (
	statusReport     = "\x1b[0n"
	deviceAttributes = "\x1b[?1;2c"
)

// lineDrawing is the DEC Special Graphics character set: what each character
// from 0x5f to 0x7e draws while it is the one in use.
var lineDrawing = []rune(" ◆▒␉␌␍␊°±␤␋┘┐┌└┼⎺⎻─⎼⎽├┤┴┬│≤≥π≠£·")

// colorQueries are the OSC commands whose "?" asks the terminal for a
// colour, or for the clipboard.
var colorQueries = map[int]bool{4: true, 5: true, 10: true, 11: true, 12: true, 13: true, 14: true, 15: true, 16: true, 17: true, 18: true, 19: true, 52: true}

// unmodeled are the DEC private modes a repaint does not set as the
// program left them: the ones the screen acts on itself, and a column
// switch, which would resize the viewer.
var unmodeled = map[int]bool{3: true, 6: true, 7: true, 25: true, 47: true, 1047: true, 1048: true, 1049: true}

// Print draws a character at the cursor. A C1 control decoded as a
// character draws nothing, since xterm.js takes it as a control.
func (s *Screen) Print(r rune) {
	if r >= 0x80 && r < 0xa0 {
		return
	}
	if r >= 0x5f && r <= 0x7e && s.charsets[s.shift] {
		r = lineDrawing[r-0x5f]
	}
	s.lastPrinted = r
	switch width := runeWidth(r); width {
	case 0:
		s.combine(r)
	default:
		s.printCell(r, width)
	}
}

// printCell draws r, width cells wide, at the cursor and moves the cursor
// past it.
func (s *Screen) printCell(r rune, width int) {
	g := s.screen()
	if s.cursor.isWrapPending && s.isAutowrap {
		s.wrap()
	}
	if width == 2 && s.cursor.x == s.cols-1 {
		if !s.isAutowrap {
			return
		}
		g.erase(s.cursor.y, s.cursor.x, s.cols, s.pen)
		s.wrap()
	}
	if s.isInsert {
		g.insertCells(s.cursor.y, s.cursor.x, width, s.pen)
	}
	g.put(s.cursor.y, s.cursor.x, cell{char: r, width: uint8(width), style: s.pen})
	if s.cursor.x+width >= s.cols {
		s.cursor.x = s.cols - 1
		s.cursor.isWrapPending = s.isAutowrap
		return
	}
	s.cursor.x += width
	s.cursor.isWrapPending = false
}

// maxMarks bounds the marks one cell keeps, so output of nothing but marks
// costs neither memory nor time without end.
const maxMarks = 32

// combine draws r, a mark of no width, over the character before the
// cursor.
func (s *Screen) combine(r rune) {
	x := s.cursor.x - 1
	if s.cursor.isWrapPending {
		x = s.cursor.x
	}
	line := s.screen().rows[s.cursor.y].cells
	if x > 0 && line[x].width == 0 {
		x--
	}
	if x < 0 || line[x].char == 0 || len(line[x].marks)+utf8.RuneLen(r) > maxMarks {
		return
	}
	line[x].marks += string(r)
}

// newLine moves the cursor to the start of the next row, scrolling at the
// bottom of the scrolling region.
func (s *Screen) newLine() {
	s.cursor.x = 0
	s.index()
}

// wrap goes on to the next row as output reaching the edge does, which
// marks that row as the one before it continued.
func (s *Screen) wrap() {
	s.newLine()
	s.screen().rows[s.cursor.y].isWrapped = true
}

// index moves the cursor down a row, scrolling the region up at its
// bottom.
func (s *Screen) index() {
	s.cursor.isWrapPending = false
	switch {
	case s.cursor.y == s.bottom:
		s.screen().scrollUp(s.top, s.bottom, 1, s.pen)
	case s.cursor.y < s.rows-1:
		s.cursor.y++
	}
}

// reverseIndex moves the cursor up a row, scrolling the region down at its
// top.
func (s *Screen) reverseIndex() {
	s.cursor.isWrapPending = false
	switch {
	case s.cursor.y == s.top:
		s.screen().scrollDown(s.top, s.bottom, 1, s.pen)
	case s.cursor.y > 0:
		s.cursor.y--
	}
}

// Execute acts on a control character.
func (s *Screen) Execute(b byte) {
	switch b {
	case '\b':
		s.cursor.isWrapPending = false
		s.cursor.x = max(0, s.cursor.x-1)
	case '\t':
		s.tab(1)
	case '\n', '\v', '\f':
		s.index()
		if s.isNewlineMode {
			s.cursor.x = 0
		}
	case '\r':
		s.cursor.x, s.cursor.isWrapPending = 0, false
	case 0x0e:
		s.shift = 1
	case 0x0f:
		s.shift = 0
	}
}

// tab moves the cursor n tab stops right, or back for a negative n, never
// past the screen's edge. With a wrap pending it does nothing, as in
// xterm.js.
func (s *Screen) tab(n int) {
	if s.cursor.isWrapPending {
		return
	}
	for ; n > 0 && s.cursor.x < s.cols-1; n-- {
		for s.cursor.x++; s.cursor.x < s.cols-1 && !s.tabs[s.cursor.x]; s.cursor.x++ {
		}
	}
	for ; n < 0 && s.cursor.x > 0; n++ {
		for s.cursor.x--; s.cursor.x > 0 && !s.tabs[s.cursor.x]; s.cursor.x-- {
		}
	}
}

// Put, Unhook and SosPmApcDispatch take a device control string's data, its
// end, and the strings the screen passes on without reading.
func (s *Screen) Put(byte)                                        {}
func (s *Screen) Unhook()                                         {}
func (s *Screen) SosPmApcDispatch(vte.SosPmApcKind, []byte, bool) {}

// Hook starts a device control string: DECRQSS and XTGETTCAP ask the
// terminal for its settings and capabilities, which the screen keeps from
// viewers unanswered.
func (s *Screen) Hook(params [][]uint16, intermediates []byte, ignore bool, r rune) {
	if r == 'q' && (bytes.Equal(intermediates, []byte("$")) || bytes.Equal(intermediates, []byte("+"))) {
		s.isDropped = true
	}
}

// OscDispatch reads an operating system command: one that asks for a colour
// or the clipboard is kept from viewers unanswered, since a viewer's answer
// would reach the program as typed input, and the clipboard is the viewer's
// own.
func (s *Screen) OscDispatch(params [][]byte, bellTerminated bool) {
	if len(params) < 2 {
		return
	}
	if command, err := strconv.Atoi(string(params[0])); err != nil || !colorQueries[command] {
		return
	}
	for _, param := range params[1:] {
		if bytes.Equal(param, []byte("?")) {
			s.isDropped = true
		}
	}
}

// EscDispatch acts on an escape sequence.
func (s *Screen) EscDispatch(intermediates []byte, ignore bool, b byte) {
	if ignore {
		return
	}
	if len(intermediates) == 1 {
		switch set := intermediates[0]; {
		case set >= '(' && set <= '+':
			s.charsets[set-'('] = b == '0'
		case set == '#' && b == '8':
			s.alignmentTest()
		}
		return
	}
	if len(intermediates) > 0 {
		return
	}
	switch b {
	case '7':
		s.saveCursor()
	case '8':
		s.restoreCursor()
	case 'D':
		s.index()
	case 'E':
		s.newLine()
	case 'M':
		s.reverseIndex()
	case 'H':
		s.tabs[s.cursor.x] = true
	case 'c':
		s.reset()
	case '=':
		s.isKeypadMode = true
	case '>':
		s.isKeypadMode = false
	}
}

// alignmentTest is DECALN: the screen filled with E, the margins reset and
// the cursor home.
func (s *Screen) alignmentTest() {
	g := s.screen()
	for y := range g.rows {
		g.rows[y].isWrapped = false
		for x := range g.rows[y].cells {
			g.rows[y].cells[x] = cell{char: 'E', width: 1}
		}
	}
	s.top, s.bottom = 0, s.rows-1
	s.cursor = cursor{}
}

func (s *Screen) saveCursor() {
	s.saved[s.screenIndex()] = savedCursor{cursor: s.cursor, style: s.pen, isOriginMode: s.isOriginMode, charsets: s.charsets, shift: s.shift}
}

func (s *Screen) restoreCursor() {
	saved := s.saved[s.screenIndex()]
	s.cursor, s.pen, s.isOriginMode, s.charsets, s.shift = saved.cursor, saved.style, saved.isOriginMode, saved.charsets, saved.shift
}

func (s *Screen) screenIndex() int {
	if s.isAlt {
		return 1
	}
	return 0
}

// CsiDispatch acts on a control sequence, answering and keeping from
// viewers the ones that ask the terminal something.
func (s *Screen) CsiDispatch(params [][]uint16, intermediates []byte, ignore bool, r rune) {
	if ignore {
		return
	}
	var marker, intermediate byte
	for _, b := range intermediates {
		if b >= '<' && b <= '?' {
			marker = b
		} else {
			intermediate = b
		}
	}
	switch {
	case marker == 0 && intermediate == 0:
		s.control(params, r)
	case marker == '?' && intermediate == 0:
		s.privateControl(params, r)
	case marker == '>' && (r == 'c' || r == 'q'), marker == '=' && r == 'c':
		// The secondary and tertiary device attributes and the terminal's
		// name and version.
		s.isDropped = true
	case intermediate == '$' && r == 'p', intermediate == '$' && r == 'w':
		// DECRQM asks whether a mode is set, and DECRQPSR for a report.
		s.isDropped = true
	case intermediate == ' ' && r == 'q':
		s.cursorStyle = param(params, 0, 0)
	case intermediate == '!' && r == 'p':
		s.softReset()
	}
}

// param is params' i-th value, or otherwise when it is missing or 0.
func param(params [][]uint16, i, otherwise int) int {
	if i >= len(params) || params[i][0] == 0 {
		return otherwise
	}
	return int(params[i][0])
}

// control acts on a control sequence with no private marker.
func (s *Screen) control(params [][]uint16, r rune) {
	g := s.screen()
	n := param(params, 0, 1)
	switch r {
	case '@':
		s.cursor.isWrapPending = false
		g.insertCells(s.cursor.y, s.cursor.x, n, s.pen)
	case 'A':
		s.moveUp(n)
	case 'B', 'e':
		s.moveDown(n)
	case 'C', 'a':
		s.cursor.x, s.cursor.isWrapPending = min(s.cols-1, s.cursor.x+n), false
	case 'D':
		s.cursor.x, s.cursor.isWrapPending = max(0, s.cursor.x-n), false
	case 'E':
		s.moveDown(n)
		s.cursor.x = 0
	case 'F':
		s.moveUp(n)
		s.cursor.x = 0
	case 'G', '`':
		s.cursor.x, s.cursor.isWrapPending = min(s.cols-1, n-1), false
	case 'H', 'f':
		s.moveTo(param(params, 0, 1)-1, param(params, 1, 1)-1)
	case 'I':
		s.tab(n)
	case 'Z':
		s.tab(-n)
	case 'J':
		s.eraseDisplay(param(params, 0, 0))
	case 'K':
		s.eraseLine(param(params, 0, 0))
	case 'L', 'M':
		s.cursor.isWrapPending = false
		if s.cursor.y < s.top || s.cursor.y > s.bottom {
			return
		}
		if r == 'L' {
			g.scrollDown(s.cursor.y, s.bottom, n, s.pen)
		} else {
			g.scrollUp(s.cursor.y, s.bottom, n, s.pen)
		}
		s.cursor.x = 0
	case 'P':
		s.cursor.isWrapPending = false
		g.deleteCells(s.cursor.y, s.cursor.x, n, s.pen)
	case 'X':
		s.cursor.isWrapPending = false
		g.erase(s.cursor.y, s.cursor.x, s.cursor.x+n, s.pen)
	case 'S':
		g.scrollUp(s.top, s.bottom, n, s.pen)
	case 'T':
		if len(params) <= 1 {
			g.scrollDown(s.top, s.bottom, n, s.pen)
		}
	case 'b':
		if s.lastPrinted != 0 {
			for range min(n, s.cols*s.rows) {
				s.Print(s.lastPrinted)
			}
		}
	case 'd':
		s.moveTo(n-1, s.cursor.x)
	case 'g':
		switch param(params, 0, 0) {
		case 0:
			s.tabs[s.cursor.x] = false
		case 3:
			clear(s.tabs)
		}
	case 'h', 'l':
		for i := range params {
			switch param(params, i, 0) {
			case 4:
				s.isInsert = r == 'h'
			case 20:
				s.isNewlineMode = r == 'h'
			}
		}
	case 'm':
		s.pen.apply(params)
	case 'n':
		switch param(params, 0, 0) {
		case 5:
			s.answers = append(s.answers, statusReport...)
		case 6:
			s.answers = append(s.answers, s.cursorReport(false)...)
		}
		s.isDropped = true
	case 'c':
		if param(params, 0, 0) == 0 {
			s.answers = append(s.answers, deviceAttributes...)
		}
		s.isDropped = true
	case 'r':
		top, bottom := param(params, 0, 1)-1, min(param(params, 1, s.rows), s.rows)-1
		if top < bottom {
			s.top, s.bottom = top, bottom
			s.moveTo(0, 0)
		}
	case 's':
		s.saveCursor()
	case 'u':
		s.restoreCursor()
	case 't':
		// Window manipulations from 11 on report the window, which no
		// viewer answers for the program.
		if param(params, 0, 0) >= 11 && param(params, 0, 0) != 22 && param(params, 0, 0) != 23 {
			s.isDropped = true
		}
	case 'x':
		s.isDropped = true
	}
}

// privateControl acts on a control sequence with the ? marker.
func (s *Screen) privateControl(params [][]uint16, r rune) {
	switch r {
	case 'h', 'l':
		for i := range params {
			s.setMode(param(params, i, 0), r == 'h')
		}
	case 'J':
		s.eraseDisplay(param(params, 0, 0))
	case 'K':
		s.eraseLine(param(params, 0, 0))
	case 'n':
		if param(params, 0, 0) == 6 {
			s.answers = append(s.answers, s.cursorReport(true)...)
		}
		s.isDropped = true
	case 'u':
		// The kitty keyboard protocol's query, which no viewer here
		// supports.
		s.isDropped = true
	}
}

// setMode sets or resets DEC private mode.
func (s *Screen) setMode(mode int, isSet bool) {
	switch mode {
	case 6:
		s.isOriginMode = isSet
		s.moveTo(0, 0)
	case 7:
		s.isAutowrap = isSet
		if !isSet {
			s.cursor.isWrapPending = false
		}
	case 25:
		s.isCursorHidden = !isSet
	case 47, 1047, 1049:
		// As in xterm.js, every switch clears the alternate screen, and
		// leaving with 1049 restores the saved cursor even on the main
		// screen.
		if mode == 1049 && isSet && !s.isAlt {
			s.saveCursor()
		}
		if isSet != s.isAlt {
			s.isAlt = isSet
			s.alt = newGrid(s.cols, s.rows)
		}
		if mode == 1049 && !isSet {
			s.restoreCursor()
		}
	case 1048:
		if isSet {
			s.saveCursor()
		} else {
			s.restoreCursor()
		}
	}
	if unmodeled[mode] || mode == 0 {
		return
	}
	if _, seen := s.modes[mode]; !seen {
		s.modeOrder = append(s.modeOrder, mode)
	}
	s.modes[mode] = isSet
}

// softReset is DECSTR: the modes, margins, style and saved cursor of a
// terminal that starts, with the screen left as it is. As in xterm.js, it
// resets every DEC private mode the program set.
func (s *Screen) softReset() {
	s.isInsert, s.isOriginMode, s.isAutowrap, s.isCursorHidden, s.isKeypadMode = false, false, true, false, false
	s.top, s.bottom = 0, s.rows-1
	s.pen = style{}
	s.charsets, s.shift = [4]bool{}, 0
	s.saved = [2]savedCursor{}
	s.cursor.isWrapPending = false
	s.modes, s.modeOrder = map[int]bool{}, nil
}

// moveUp moves the cursor up n rows, stopping at the scrolling region's top
// from inside it.
func (s *Screen) moveUp(n int) {
	limit := 0
	if s.cursor.y >= s.top {
		limit = s.top
	}
	s.cursor.y, s.cursor.isWrapPending = max(limit, s.cursor.y-n), false
}

// moveDown moves the cursor down n rows, stopping at the scrolling region's
// bottom from inside it.
func (s *Screen) moveDown(n int) {
	limit := s.rows - 1
	if s.cursor.y <= s.bottom {
		limit = s.bottom
	}
	s.cursor.y, s.cursor.isWrapPending = min(limit, s.cursor.y+n), false
}

// moveTo moves the cursor to row y and column x, from 0, counted in origin
// mode from the scrolling region's top and kept inside it.
func (s *Screen) moveTo(y, x int) {
	top, bottom := 0, s.rows-1
	if s.isOriginMode {
		top, bottom = s.top, s.bottom
	}
	s.cursor.y = min(bottom, max(top, top+y))
	s.cursor.x = min(s.cols-1, max(0, x))
	s.cursor.isWrapPending = false
}

// eraseDisplay is ED. A row erased from its start no longer continues the
// row above it, as in xterm.js.
func (s *Screen) eraseDisplay(mode int) {
	g := s.screen()
	switch mode {
	case 0:
		s.eraseLine(0)
		for y := s.cursor.y + 1; y < s.rows; y++ {
			g.erase(y, 0, s.cols, s.pen)
			g.rows[y].isWrapped = false
		}
	case 1:
		for y := 0; y < s.cursor.y; y++ {
			g.erase(y, 0, s.cols, s.pen)
			g.rows[y].isWrapped = false
		}
		g.erase(s.cursor.y, 0, s.cursor.x+1, s.pen)
		g.rows[s.cursor.y].isWrapped = false
		if s.cursor.x+1 >= s.cols && s.cursor.y+1 < s.rows {
			g.rows[s.cursor.y+1].isWrapped = false
		}
	case 2:
		for y := range s.rows {
			g.erase(y, 0, s.cols, s.pen)
			g.rows[y].isWrapped = false
		}
	}
}

// eraseLine is EL. A row erased from its start no longer continues the row
// above it, as in xterm.js.
func (s *Screen) eraseLine(mode int) {
	g := s.screen()
	switch mode {
	case 0:
		from := s.eraseFrom()
		g.erase(s.cursor.y, from, s.cols, s.pen)
		if from == 0 {
			g.rows[s.cursor.y].isWrapped = false
		}
	case 1:
		g.erase(s.cursor.y, 0, s.cursor.x+1, s.pen)
	case 2:
		g.erase(s.cursor.y, 0, s.cols, s.pen)
		g.rows[s.cursor.y].isWrapped = false
	}
}

// eraseFrom is the column an erase to the end of the row starts at: the
// cursor's, or past the row's end with a wrap pending, as xterm.js erases,
// which keeps the character a full row ends with.
func (s *Screen) eraseFrom() int {
	if s.cursor.isWrapPending {
		return s.cols
	}
	return s.cursor.x
}
