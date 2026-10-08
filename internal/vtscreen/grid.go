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

// row is one row of a screen. isWrapped says it continues the row above:
// the program's output reached the edge there and went on here.
type row struct {
	cells     []cell
	isWrapped bool
}

// grid is one screen's rows.
type grid struct {
	rows []row
}

func newGrid(cols, rows int) *grid {
	g := &grid{rows: make([]row, rows)}
	for y := range g.rows {
		g.rows[y] = row{cells: blankLine(cols, style{})}
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
	line := g.rows[y].cells
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
	line := g.rows[y].cells
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
	line := g.rows[y].cells
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
	line := g.rows[y].cells
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
	line := g.rows[y].cells
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
	region := g.rows[top : bottom+1]
	n = min(n, len(region))
	rotate(region, n)
	blankRows(region[len(region)-n:], s)
}

// scrollDown moves rows top to bottom down by n, losing the bottom n, and
// blanks the n rows that open at the top, reusing the lost rows' cells.
func (g *grid) scrollDown(top, bottom, n int, s style) {
	region := g.rows[top : bottom+1]
	n = min(n, len(region))
	rotate(region, len(region)-n)
	blankRows(region[:n], s)
}

// rotate moves rows left by n, the first n going to the end.
func rotate(rows []row, n int) {
	slices.Reverse(rows[:n])
	slices.Reverse(rows[n:])
	slices.Reverse(rows)
}

// blankRows blanks rows in s's background, as rows a scroll opens are.
func blankRows(rows []row, s style) {
	for y := range rows {
		rows[y].isWrapped = false
		for x := range rows[y].cells {
			rows[y].cells[x] = blank(s)
		}
	}
}

// fit gives the grid cols and rows, the way xterm.js resizes a screen it
// does not reflow, and returns where the row cursorY moved to: a smaller
// screen loses its bottom rows while any lies below the cursor's, then its
// top rows, and a larger one gains blank rows at its bottom. Each row loses
// or gains cells at its end.
func (g *grid) fit(cols, rows, cursorY int) int {
	for len(g.rows) > rows {
		if len(g.rows)-1 > cursorY {
			g.rows = g.rows[:len(g.rows)-1]
			continue
		}
		g.rows = g.rows[1:]
		cursorY--
	}
	for len(g.rows) < rows {
		g.rows = append(g.rows, row{cells: blankLine(cols, style{})})
	}
	for y, r := range g.rows {
		switch {
		case len(r.cells) > cols:
			r.cells = r.cells[:cols:cols]
			if r.cells[cols-1].width == 2 {
				r.cells[cols-1] = blank(r.cells[cols-1].style)
			}
		case len(r.cells) < cols:
			r.cells = append(r.cells, blankLine(cols-len(r.cells), style{})...)
		}
		g.rows[y] = r
	}
	if len(g.rows) > 0 {
		g.rows[0].isWrapped = false
	}
	return max(0, cursorY)
}

// reflow wraps the grid's rows again at cols, as xterm.js reflows a screen
// it keeps scrollback for: the rows a wrap joined are one line again and
// that line is wrapped anew at cols, all but the line the cursor is on,
// which xterm.js leaves to the program and fit cuts or fills like any row.
// It returns the row the cursor at row y is on now, among as many rows as
// the lines now take, which fit then fits to the screen's height.
func (g *grid) reflow(cols, y int) int {
	var reflowed []row
	cursorY := y
	for start := 0; start < len(g.rows); {
		end := start + 1
		for end < len(g.rows) && g.rows[end].isWrapped {
			end++
		}
		if y >= start && y < end {
			cursorY = len(reflowed) + y - start
			reflowed = append(reflowed, g.rows[start:end]...)
			start = end
			continue
		}
		var cells []cell
		for _, r := range g.rows[start:end] {
			cells = append(cells, r.cells...)
		}
		length := len(cells)
		for length > 0 && cells[length-1] == blank(style{}) {
			length--
		}
		reflowed = append(reflowed, wrap(cells[:length], cols, g.rows[start].isWrapped)...)
		start = end
	}
	g.rows = reflowed
	return cursorY
}

// wrap lays cells out on rows of cols, a wide character that would cross
// the edge going to the next row, the first row marked continued when
// isWrapped says the line it starts was.
func wrap(cells []cell, cols int, isWrapped bool) []row {
	rows := []row{{cells: blankLine(cols, style{}), isWrapped: isWrapped}}
	x := 0
	for _, c := range cells {
		if c.width == 0 {
			continue
		}
		if x+int(c.width) > cols {
			rows = append(rows, row{cells: blankLine(cols, style{}), isWrapped: true})
			x = 0
		}
		rows[len(rows)-1].cells[x] = c
		if c.width == 2 {
			rows[len(rows)-1].cells[x+1] = cell{style: c.style}
		}
		x += int(c.width)
	}
	return rows
}
