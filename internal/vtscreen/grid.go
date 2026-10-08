package vtscreen

import (
	"slices"
	"unicode/utf8"
)

// cell is one character cell: what it shows and how. A wide character
// takes its cell, with width 2, and the next, a continuation with width 0
// and no character. A blank cell has no character. marks are the marks of
// no width drawn over the character, such as accents.
type cell struct {
	char  rune
	marks string
	width uint8
	style style
}

// appendShown appends what c shows, a space for a blank.
func (c cell) appendShown(out []byte) []byte {
	if c.char == 0 {
		return append(out, ' ')
	}
	return append(utf8.AppendRune(out, c.char), c.marks...)
}

// blank is an empty cell drawn in s's background, as erasing leaves it.
func blank(s style) cell {
	return cell{width: 1, style: style{background: s.background}}
}

// grid is one screen's cells, a line per row.
type grid struct {
	lines [][]cell
}

func newGrid(cols, rows int) *grid {
	g := &grid{lines: make([][]cell, rows)}
	for y := range g.lines {
		g.lines[y] = blankLine(cols, style{})
	}
	return g
}

func blankLine(cols int, s style) []cell {
	line := make([]cell, cols)
	for x := range line {
		line[x] = blank(s)
	}
	return line
}

// put writes c at row y, column x, first erasing whatever wide character
// either cell it covers belonged to.
func (g *grid) put(y, x int, c cell) {
	line := g.lines[y]
	g.splitWide(y, x)
	if c.width == 2 && x+1 < len(line) {
		g.splitWide(y, x+1)
		line[x+1] = cell{style: c.style}
	}
	line[x] = c
}

// splitWide blanks the wide character the cell at row y, column x is half
// of, so a write to one half never leaves the other drawn.
func (g *grid) splitWide(y, x int) {
	line := g.lines[y]
	switch {
	case line[x].width == 0 && x > 0:
		line[x-1] = blank(line[x-1].style)
		line[x] = blank(line[x].style)
	case line[x].width == 2 && x+1 < len(line):
		line[x+1] = blank(line[x+1].style)
	}
}

// erase blanks the cells of row y from column from up to but not including
// column to, in s's background.
func (g *grid) erase(y, from, to int, s style) {
	line := g.lines[y]
	from, to = max(0, from), min(len(line), to)
	if from >= to {
		return
	}
	g.splitWide(y, from)
	g.splitWide(y, to-1)
	for x := from; x < to; x++ {
		line[x] = blank(s)
	}
}

// insertCells shifts row y right of column x by n cells, which fall off its
// end, and blanks the n cells that open.
func (g *grid) insertCells(y, x, n int, s style) {
	line := g.lines[y]
	n = min(n, len(line)-x)
	g.splitWide(y, x)
	copy(line[x+n:], line[x:len(line)-n])
	for i := x; i < x+n; i++ {
		line[i] = blank(s)
	}
	if last := len(line) - 1; line[last].width == 2 {
		line[last] = blank(line[last].style)
	}
}

// deleteCells shifts row y left onto column x by n cells and blanks the n
// cells that open at its end.
func (g *grid) deleteCells(y, x, n int, s style) {
	line := g.lines[y]
	n = min(n, len(line)-x)
	g.splitWide(y, x)
	g.splitWide(y, min(len(line)-1, x+n))
	copy(line[x:], line[x+n:])
	for i := len(line) - n; i < len(line); i++ {
		line[i] = blank(s)
	}
}

// scrollUp moves rows top to bottom up by n, losing the top n, and blanks
// the n rows that open at the bottom, reusing the lost rows' cells.
func (g *grid) scrollUp(top, bottom, n int, s style) {
	region := g.lines[top : bottom+1]
	n = min(n, len(region))
	rotate(region, n)
	g.blankRows(region[len(region)-n:], s)
}

// scrollDown moves rows top to bottom down by n, losing the bottom n, and
// blanks the n rows that open at the top, reusing the lost rows' cells.
func (g *grid) scrollDown(top, bottom, n int, s style) {
	region := g.lines[top : bottom+1]
	n = min(n, len(region))
	rotate(region, len(region)-n)
	g.blankRows(region[:n], s)
}

// rotate moves lines left by n, the first n going to the end.
func rotate(lines [][]cell, n int) {
	slices.Reverse(lines[:n])
	slices.Reverse(lines[n:])
	slices.Reverse(lines)
}

func (g *grid) blankRows(lines [][]cell, s style) {
	for _, line := range lines {
		for x := range line {
			line[x] = blank(s)
		}
	}
}

// resize gives the grid cols and rows, the way xterm.js resizes its screen,
// and returns where the row cursorY moved to: a smaller screen loses its
// bottom rows while any lies below the cursor's, then its top rows, and a
// larger one gains blank rows at its bottom. Each row loses or gains cells
// at its end.
func (g *grid) resize(cols, rows, cursorY int) int {
	for len(g.lines) > rows {
		if len(g.lines)-1 > cursorY {
			g.lines = g.lines[:len(g.lines)-1]
			continue
		}
		g.lines = g.lines[1:]
		cursorY--
	}
	for len(g.lines) < rows {
		g.lines = append(g.lines, blankLine(cols, style{}))
	}
	for y, line := range g.lines {
		switch {
		case len(line) > cols:
			line = line[:cols:cols]
			if line[cols-1].width == 2 {
				line[cols-1] = blank(line[cols-1].style)
			}
		case len(line) < cols:
			line = append(line, blankLine(cols-len(line), style{})...)
		}
		g.lines[y] = line
	}
	return max(0, cursorY)
}
