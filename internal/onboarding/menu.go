package onboarding

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ErrNoConsole says the quick start has no console to ask in.
var ErrNoConsole = errors.New("the quick start asks in a terminal, and this command has none; run goblins in a terminal window")

// Key is one key a menu reads: one of the named keys below, or a letter as
// its lower-case rune.
type Key rune

// The keys a menu acts on besides letters. They sit outside Unicode, so no
// letter is one of them.
const (
	KeyEnter Key = -(iota + 1)
	KeyUp
	KeyDown
	KeyEscape
	KeyLeft
	KeyRight
)

// Choice is one choice of a step. Key, when set, is a letter that accepts the
// choice at once, and the step says so. In a row of tabs, Mark is the
// choice's own mark, drawn before its label, and Note is the line shown under
// the row while the choice is marked.
type Choice struct {
	Label string
	Key   Key
	Mark  string
	Note  string
}

// Step is one screen that waits on the person: a title, the lines under it,
// and its choices with one marked as the default. Tabs lays the choices out
// as one row of tabs, moved with Left and Right, instead of a row each.
type Step struct {
	Title    string
	Detail   string
	Choices  []Choice
	Selected int
	Tabs     bool
}

// Menu shows the steps that wait on the person. Enter accepts the marked
// choice, so every step has one clear way on, and a step answered takes its
// screen with it, so the steps before never pile up above the next.
type Menu struct {
	Output io.Writer
	// ReadKey waits for the next key. An error means no key can come, as at
	// the end of input.
	ReadKey func() (Key, error)
	// Width is the console's width in columns, onto which a wider row wraps.
	// With none, or a width of 0 or less, every row is one line.
	Width func() int
	// NoColor leaves the colours out, as NO_COLOR asks.
	NoColor bool
}

// The quick start's own greens, the board's accent and mint.
const (
	menuAccent = "\x1b[38;2;0;229;155m"
	menuMarked = "\x1b[48;2;15;24;32m\x1b[38;2;110;231;183m"
	menuReset  = "\x1b[0m"
)

// painted is text in colour, or text alone where colour is left out.
func painted(colour, text string, noColor bool) string {
	if noColor {
		return text
	}
	return colour + text + menuReset
}

// Ask shows step and returns the choice the person accepts: the marked one
// with Enter, moved with the arrows, or a choice's own letter. Escape returns
// ErrBack. Either way the step's screen is erased, back to where it began.
// When no key can be read it returns ErrCancelled and leaves the screen, so
// input that ended never accepts a default and shows what it was asked.
func (m Menu) Ask(step Step) (int, error) {
	choices, selected := step.Choices, step.Selected
	if len(choices) == 0 || selected < 0 || selected >= len(choices) {
		return 0, errors.New("a step needs choices and a default among them")
	}
	width := 0
	if m.Width != nil {
		width = m.Width()
	}
	// The row the cursor is on may hold a line still being worked on, such
	// as "Checking ...": the step takes its place.
	fmt.Fprintf(m.Output, "\r\x1b[2K\n%s\n", painted(menuAccent, step.Title, m.NoColor))
	above := 1 + lines(step.Title, width)
	if step.Detail != "" {
		for _, line := range strings.Split(step.Detail, "\n") {
			fmt.Fprintf(m.Output, "%s\n", line)
			above += lines(line, width)
		}
	}
	fmt.Fprintln(m.Output)
	above++
	hint := "Press Enter to continue"
	for _, choice := range choices {
		if choice.Key != 0 {
			hint += fmt.Sprintf("   %c: %s", unicode.ToUpper(rune(choice.Key)), choice.Label)
		}
	}
	switch {
	case len(choices) == 1:
	case step.Tabs:
		hint += "   Left/Right to choose"
	default:
		hint += "   Up/Down to choose"
	}
	hint += "   Esc to go back"
	for {
		// \x1b[2K clears a row, so a redraw leaves nothing of the last.
		drawn := 0
		if step.Tabs {
			drawn = m.drawTabs(choices, selected, width)
		} else {
			for index, choice := range choices {
				row := "    " + choice.Label
				if index == selected {
					row = painted(menuMarked, "  > "+choice.Label, m.NoColor)
				}
				fmt.Fprintf(m.Output, "\x1b[2K%s\n", row)
				drawn += lines(row, width)
			}
		}
		fmt.Fprintf(m.Output, "\x1b[2K\n\x1b[2K%s\n", hint)
		drawn += 1 + lines(hint, width)
		key, err := m.ReadKey()
		if err != nil {
			return 0, ErrCancelled
		}
		accepted := -1
		switch key {
		case KeyEnter:
			accepted = selected
		case KeyEscape:
			m.erase(above + drawn)
			return 0, ErrBack
		case KeyUp, KeyLeft:
			selected = (selected + len(choices) - 1) % len(choices)
		case KeyDown, KeyRight:
			selected = (selected + 1) % len(choices)
		default:
			for index, choice := range choices {
				if choice.Key != 0 && choice.Key == key {
					accepted = index
				}
			}
		}
		if accepted >= 0 {
			m.erase(above + drawn)
			return accepted, nil
		}
		// Back to the first choice's row, to draw the choices again in place.
		fmt.Fprintf(m.Output, "\x1b[%dA", drawn)
	}
}

// drawTabs draws choices as one row of tabs with selected in brackets, which
// show the marked tab where colour does not, and under it that tab's note.
// It returns how many lines it drew.
func (m Menu) drawTabs(choices []Choice, selected, width int) int {
	row := " "
	for index, choice := range choices {
		label := choice.Label
		if choice.Mark != "" {
			label = choice.Mark + " " + label
		}
		if index == selected {
			row += painted(menuMarked, "[ "+label+" ]", m.NoColor)
		} else {
			row += "  " + label + "  "
		}
		if index < len(choices)-1 {
			row += " "
		}
	}
	note := "   " + choices[selected].Note
	fmt.Fprintf(m.Output, "\x1b[2K%s\n\x1b[2K%s\n", row, note)
	return lines(row, width) + lines(note, width)
}

// erase clears the count lines above the cursor and everything under them,
// and leaves the cursor at the first of them.
func (m Menu) erase(count int) {
	fmt.Fprintf(m.Output, "\x1b[%dA\r\x1b[J", count)
}

// escapeSequence is a sequence the console acts on and draws nothing for: a
// CSI sequence, such as a colour, or an OSC sequence, such as a link, up to
// its terminator.
var escapeSequence = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\a\x1b]*(?:\a|\x1b\\)`)

// lines is how many lines row takes in a console width columns wide, by the
// columns it takes on the screen: its escape sequences take none.
func lines(row string, width int) int {
	if width <= 0 {
		return 1
	}
	columns := utf8.RuneCountInString(escapeSequence.ReplaceAllString(row, ""))
	return max(1, (columns+width-1)/width)
}

// Labels are choices with no letters of their own.
func Labels(labels ...string) []Choice {
	choices := make([]Choice, len(labels))
	for index, label := range labels {
		choices[index] = Choice{Label: label}
	}
	return choices
}

// Checklist prints the steps the quick start has finished, one line each: a
// tick, the step's name and its answer. Working shows the step under way on
// the row the next line replaces, and Clear takes the finished lines back,
// for a step back to the first choice.
type Checklist struct {
	Output io.Writer
	// Tick is the mark a finished line starts with.
	Tick string
	// Width is the console's width in columns, as Menu's.
	Width func() int
	// Plain is set for output that is not a console, such as a pipe or a
	// log: every line is printed whole and none is replaced or erased.
	Plain bool
	// NoColor leaves the tick's colour out, as NO_COLOR asks.
	NoColor bool
	drawn   int
	// working is whether a Working line holds the cursor's row.
	working bool
}

// checklistName is the width a step's name is padded to, so every answer
// starts in the same column.
const checklistName = 10

// Done prints one finished line in place of any line still being worked on.
func (c *Checklist) Done(name, answer string) {
	row := fmt.Sprintf("  %s %-*s %s", c.Tick, checklistName, name, answer)
	if c.Plain {
		fmt.Fprintln(c.Output, row)
		return
	}
	fmt.Fprintf(c.Output, "\r\x1b[2K  %s %-*s %s\n", painted(menuAccent, c.Tick, c.NoColor), checklistName, name, answer)
	c.drawn += lines(row, c.width())
	c.working = false
}

// Working shows what is under way with no line end, so the line that follows
// takes its place.
func (c *Checklist) Working(name, doing string) {
	row := fmt.Sprintf("  %s %-*s %s", strings.Repeat(".", utf8.RuneCountInString(c.Tick)), checklistName, name, doing)
	if c.Plain {
		fmt.Fprintln(c.Output, row)
		return
	}
	fmt.Fprintf(c.Output, "\r\x1b[2K%s", row)
	c.working = true
}

// End ends the row a Working line left open, so what is printed next starts
// on a row of its own. Plain output's line was whole already.
func (c *Checklist) End() {
	if c.working {
		fmt.Fprintln(c.Output)
	}
	c.working = false
}

// Note prints a line under the last finished one, in the answers' column.
func (c *Checklist) Note(text string) {
	row := strings.Repeat(" ", 4+utf8.RuneCountInString(c.Tick)+checklistName) + text
	if c.Plain {
		fmt.Fprintln(c.Output, row)
		return
	}
	fmt.Fprintf(c.Output, "\r\x1b[2K%s\n", row)
	c.drawn += lines(row, c.width())
	c.working = false
}

func (c *Checklist) width() int {
	if c.Width == nil {
		return 0
	}
	return c.Width()
}

// Clear erases every line the checklist printed since the last Clear or Keep.
func (c *Checklist) Clear() {
	if c.drawn > 0 {
		fmt.Fprintf(c.Output, "\x1b[%dA\r\x1b[J", c.drawn)
	}
	c.drawn = 0
}

// Keep makes the lines printed so far permanent: a later Clear leaves them.
func (c *Checklist) Keep() {
	c.drawn = 0
}
