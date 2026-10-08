package vtscreen

import (
	"bytes"
	"strings"
	"testing"
)

func newScreen(t *testing.T, cols, rows int) *Screen {
	t.Helper()
	s, err := New(cols, rows)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// The cursor report a program asks for says where its own output left the
// cursor, as a terminal would, and the query never reaches a viewer.
func TestTheCursorReportSaysWhereTheProgramsOutputLeftTheCursor(t *testing.T) {
	tests := map[string]struct {
		output string
		want   string
	}{
		"text":                                {"hello", "\x1b[1;6R"},
		"a new line":                          {"hello\r\nab", "\x1b[2;3R"},
		"a full row leaves a wrap pending":    {strings.Repeat("x", 10), "\x1b[1;10R"},
		"the pending wrap at the next letter": {strings.Repeat("x", 10) + "y", "\x1b[2;2R"},
		"wide characters":                     {"日本", "\x1b[1;5R"},
		"a wide character that does not fit":  {"123456789日", "\x1b[2;3R"},
		"a combining mark":                    {"éx", "\x1b[1;3R"},
		"an emoji":                            {"✅ok", "\x1b[1;5R"},
		"a cursor move":                       {"\x1b[4;7H", "\x1b[4;7R"},
		"moves past the screen's edge":        {"\x1b[99;99H", "\x1b[5;10R"},
		"relative moves":                      {"\x1b[3;3H\x1b[2B\x1b[A\x1b[4C\x1b[2D", "\x1b[4;5R"},
		"scrolling at the bottom":             {"a\nb\nc\nd\ne\nf\ng", "\x1b[5;8R"},
		"a tab":                               {"ab\t", "\x1b[1;9R"},
		"origin mode in a scrolling region":   {"\x1b[2;4r\x1b[?6h\x1b[2;3H", "\x1b[2;3R"},
		"a scrolling region keeps cursor ups": {"\x1b[2;4r\x1b[3;1H\x1b[9A", "\x1b[2;1R"},
		"line drawing":                        {"\x1b(0lqqk\x1b(B", "\x1b[1;5R"},
		"saved and restored":                  {"\x1b[2;2H\x1b7\x1b[5;5H\x1b8", "\x1b[2;2R"},
		"the alternate screen and back":       {"\x1b[3;3H\x1b[?1049h\x1b[H\x1b[?1049l", "\x1b[3;3R"},
		"a backspace from a pending wrap":     {strings.Repeat("x", 10) + "\b", "\x1b[1;9R"},
		"inserted lines move to the margin":   {"\x1b[2;5H\x1b[L", "\x1b[2;1R"},
		"repeated characters":                 {"a\x1b[3b", "\x1b[1;5R"},
		"a new line in newline mode":          {"\x1b[20habc\n", "\x1b[2;1R"},
		"a region past the bottom":            {"\x1b[2;99r\x1b[?6h\x1b[99;1H", "\x1b[4;1R"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			// Arrange
			s := newScreen(t, 10, 5)

			// Act
			forward, answers := s.Write([]byte(test.output + "\x1b[6n"))

			// Assert
			if string(answers) != test.want {
				t.Errorf("answered %q, want %q", answers, test.want)
			}
			if string(forward) != test.output {
				t.Errorf("passed on %q, want the output without the query, %q", forward, test.output)
			}
		})
	}
}

// Output of nothing but marks of no width, such as a run of zero-width
// spaces, keeps a bounded few on the character before them, so it costs
// neither memory nor time without end.
func TestARunOfMarksKeepsABoundedFew(t *testing.T) {
	// Arrange
	s := newScreen(t, 10, 5)

	// Act
	_, answers := s.Write([]byte("e" + strings.Repeat("​", 200000) + "\x1b[6n"))

	// Assert
	if marks := s.main.lines[0][0].marks; len(marks) > maxMarks {
		t.Errorf("the cell keeps %d bytes of marks, want at most %d", len(marks), maxMarks)
	}
	if string(answers) != "\x1b[1;2R" {
		t.Errorf("answered %q, want the cursor after the one character", answers)
	}
}

// What asks a terminal something is answered by the screen, or left
// unanswered, as the system conhost leaves it, and never passed to a viewer,
// which would type its own answer into the program, once for each viewer
// and again for each one replaying the output. What only sets something is
// passed on.
func TestQueriesAreKeptFromViewersAndAnsweredAsATerminalAnswers(t *testing.T) {
	tests := map[string]struct {
		output, forward, answers string
	}{
		"cursor position":            {"\x1b[6n", "", "\x1b[1;1R"},
		"extended cursor position":   {"\x1b[?6n", "", "\x1b[?1;1R"},
		"device attributes":          {"\x1b[c", "", "\x1b[?1;2c"},
		"device attributes with 0":   {"\x1b[0c", "", "\x1b[?1;2c"},
		"status":                     {"\x1b[5n", "", "\x1b[0n"},
		"secondary attributes":       {"\x1b[>c", "", ""},
		"tertiary attributes":        {"\x1b[=c", "", ""},
		"terminal version":           {"\x1b[>0q", "", ""},
		"kitty keyboard flags":       {"\x1b[?u", "", ""},
		"colour scheme":              {"\x1b[?996n", "", ""},
		"a private mode":             {"\x1b[?2004$p", "", ""},
		"an ANSI mode":               {"\x1b[4$p", "", ""},
		"the text area's size":       {"\x1b[18t", "", ""},
		"the background colour":      {"\x1b]11;?\a", "", ""},
		"the foreground colour":      {"\x1b]10;?\x1b\\", "", ""},
		"a palette colour":           {"\x1b]4;1;?\a", "", ""},
		"the clipboard":              {"\x1b]52;c;?\a", "", ""},
		"a setting":                  {"\x1bP$qm\x1b\\", "", ""},
		"a capability":               {"\x1bP+q544e\x1b\\", "", ""},
		"the window shown":           {"\x1b[1t", "\x1b[1t", ""},
		"a title":                    {"\x1b]0;claude\x1b\\", "\x1b]0;claude\x1b\\", ""},
		"a background colour set":    {"\x1b]11;rgb:00/00/00\a", "\x1b]11;rgb:00/00/00\a", ""},
		"kitty keyboard flags set":   {"\x1b[>7u", "\x1b[>7u", ""},
		"a mode set":                 {"\x1b[?2004h", "\x1b[?2004h", ""},
		"a cursor style":             {"\x1b[2 q", "\x1b[2 q", ""},
		"a hyperlink":                {"\x1b]8;;https://x\x1b\\a\x1b]8;;\x1b\\", "\x1b]8;;https://x\x1b\\a\x1b]8;;\x1b\\", ""},
		"text around a dropped one":  {"a\x1b[?ub", "ab", ""},
		"two queries and their text": {"x\x1b[?u\x1b[cy", "xy", "\x1b[?1;2c"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			// Arrange
			s := newScreen(t, 80, 24)

			// Act
			forward, answers := s.Write([]byte(test.output))

			// Assert
			if string(forward) != test.forward || string(answers) != test.answers {
				t.Errorf("passed on %q and answered %q, want %q and %q", forward, answers, test.forward, test.answers)
			}
		})
	}
}

// Output read in pieces can end in the middle of a query or a character:
// the start is held back until its end arrives, so the query is still kept
// from viewers and a viewer is never passed half a sequence.
func TestASequenceSplitAcrossWritesIsHeldUntilItEnds(t *testing.T) {
	tests := map[string]struct {
		pieces, forwards []string
		answers          string
	}{
		"a query":       {[]string{"ab\x1b[", "6ncd"}, []string{"ab", "cd"}, "\x1b[1;3R"},
		"a colour set":  {[]string{"\x1b[38;2;1", "0;20;30mx"}, []string{"", "\x1b[38;2;10;20;30mx"}, ""},
		"a character":   {[]string{"a\xe6\x97", "\xa5b"}, []string{"a", "日b"}, ""},
		"an OSC string": {[]string{"\x1b]0;ti", "tle\a"}, []string{"", "\x1b]0;title\a"}, ""},
		"a lone escape": {[]string{"x\x1b", "[?u", "y"}, []string{"x", "", "y"}, ""},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			// Arrange
			s := newScreen(t, 80, 24)
			var answers []byte

			// Act
			var forwards []string
			for _, piece := range test.pieces {
				forward, answered := s.Write([]byte(piece))
				forwards = append(forwards, string(forward))
				answers = append(answers, answered...)
			}

			// Assert
			if strings.Join(forwards, "|") != strings.Join(test.forwards, "|") || string(answers) != test.answers {
				t.Errorf("passed on %q and answered %q, want %q and %q", forwards, answers, test.forwards, test.answers)
			}
		})
	}
}

// A sequence that outgrows what the screen holds back, such as an image or
// a string that never ends, is passed on as it comes, and a repaint a
// resize asks for meanwhile waits for its end, so it never lands inside it.
func TestARepaintWaitsForTheEndOfASequencePassedOnAsItComes(t *testing.T) {
	// Arrange
	s := newScreen(t, 20, 5)
	start := "\x1b]9;" + strings.Repeat("a", unitLimit+10)
	forward, _ := s.Write([]byte(start))
	if string(forward) != start {
		t.Fatalf("passed on %d bytes of a %d-byte sequence that outgrew the limit, want all of it", len(forward), len(start))
	}

	// Act
	repaint, err := s.Resize(30, 6)
	if err != nil {
		t.Fatal(err)
	}
	end, _ := s.Write([]byte("bb\a"))

	// Assert
	if repaint != nil {
		t.Fatalf("the resize passed on %q during the sequence, want nothing yet", repaint)
	}
	if !bytes.HasPrefix(end, []byte("bb\a\x1b[?2026h")) || !bytes.Equal(end[3:], s.Repaint()) {
		t.Fatalf("the sequence's end passed on %q, want its end and then the repaint", end)
	}
}

// A resize while the start of a sequence is held back repaints at once,
// since what viewers were passed ends between sequences, and the held start
// follows with its end.
func TestAResizeRepaintsAtOnceWhileASequenceIsHeldBack(t *testing.T) {
	// Arrange
	s, unheld := newScreen(t, 20, 5), newScreen(t, 20, 5)
	s.Write([]byte("ab\x1b[3"))
	unheld.Write([]byte("ab"))
	want, _ := unheld.Resize(21, 5)

	// Act
	repaint, err := s.Resize(21, 5)
	forward, _ := s.Write([]byte("1mc"))

	// Assert
	if err != nil || !bytes.Equal(repaint, want) {
		t.Fatalf("the resize passed on %q (%v), want the repaint of the screen without the held start, %q", repaint, err, want)
	}
	if string(forward) != "\x1b[31mc" {
		t.Fatalf("passed on %q after the resize, want the held sequence whole", forward)
	}
}

// A resize keeps the cursor where xterm.js keeps it: rows below the cursor
// go first when the screen shrinks, then rows from the top, and rows are
// added at the bottom when it grows. The same size again repaints nothing.
func TestAResizeMovesTheCursorAsATerminalDoes(t *testing.T) {
	tests := map[string]struct {
		output     string
		cols, rows int
		want       string
	}{
		"shrink below the cursor":       {"\x1b[2;3H", 10, 3, "\x1b[2;3R"},
		"shrink with the cursor at end": {"\x1b[5;3H", 10, 3, "\x1b[3;3R"},
		"shrink part below, part above": {"\x1b[4;3H", 10, 2, "\x1b[2;3R"},
		"grow":                          {"\x1b[5;3H", 10, 8, "\x1b[5;3R"},
		"narrower than the cursor":      {"\x1b[1;9H", 4, 5, "\x1b[1;4R"},
		"wider with a wrap pending":     {strings.Repeat("x", 10), 12, 5, "\x1b[1;11R"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			// Arrange
			s := newScreen(t, 10, 5)
			s.Write([]byte(test.output))

			// Act
			repaint, err := s.Resize(test.cols, test.rows)
			_, answers := s.Write([]byte("\x1b[6n"))

			// Assert
			if err != nil || repaint == nil {
				t.Fatalf("the resize passed on %q (%v), want a repaint", repaint, err)
			}
			if string(answers) != test.want {
				t.Fatalf("answered %q after the resize, want %q", answers, test.want)
			}
		})
	}
}

// A resize to the size the screen has repaints it too, as the system conhost
// does, and changes nothing else, not even a pending wrap: a viewer sends
// its size as it connects and counts on that repaint to show the screen
// whole.
func TestAResizeToTheSameSizeRepaintsAndChangesNothing(t *testing.T) {
	// Arrange
	s, unchanged := newScreen(t, 10, 5), newScreen(t, 10, 5)
	full := []byte("\x1b[3;1H" + strings.Repeat("x", 10))
	s.Write(full)
	unchanged.Write(full)

	// Act
	repaint, err := s.Resize(10, 5)

	// Assert
	if err != nil || !bytes.Equal(repaint, unchanged.Repaint()) {
		t.Fatalf("passed on %q (%v), want the repaint of the screen as it was", repaint, err)
	}
	sameScreen(t, unchanged, s)
}

func TestASizeOutsideAPseudoConsolesIsRefused(t *testing.T) {
	for _, size := range [][2]int{{1, 5}, {5, 1}, {1001, 5}, {5, 1001}} {
		// Arrange
		s := newScreen(t, 10, 5)

		// Act
		_, resizeErr := s.Resize(size[0], size[1])
		_, newErr := New(size[0], size[1])

		// Assert
		if resizeErr == nil || newErr == nil {
			t.Errorf("size %dx%d was taken (%v, %v), want it refused", size[0], size[1], resizeErr, newErr)
		}
	}
}
