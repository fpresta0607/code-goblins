package onboarding

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

// keys is a ReadKey that returns each key in turn and then the end of input.
func keys(sequence ...Key) func() (Key, error) {
	return func() (Key, error) {
		if len(sequence) == 0 {
			return 0, io.EOF
		}
		key := sequence[0]
		sequence = sequence[1:]
		return key, nil
	}
}

// Enter accepts the marked choice, Up and Down move it and wrap, a choice's
// own letter accepts it at once, Escape goes back, and input that ends
// accepts nothing.
func TestAMenuAnswersToEnterTheArrowsALetterAndEscape(t *testing.T) {
	choices := []Choice{{Label: "Open the CFO terminal"}, {Label: "Open the board", Key: 'b'}, {Label: "Leave"}}
	for _, c := range []struct {
		name string
		keys []Key
		want int
		err  error
	}{
		{"Enter accepts the default", []Key{KeyEnter}, 0, nil},
		{"Down then Enter", []Key{KeyDown, KeyEnter}, 1, nil},
		{"Up wraps to the last", []Key{KeyUp, KeyEnter}, 2, nil},
		{"Down wraps to the first", []Key{KeyDown, KeyDown, KeyDown, KeyEnter}, 0, nil},
		{"a choice's letter", []Key{'b'}, 1, nil},
		{"a letter no choice has is ignored", []Key{'x', KeyEnter}, 0, nil},
		{"Escape goes back", []Key{KeyDown, KeyEscape}, 0, ErrBack},
		{"the end of input accepts nothing", nil, 0, ErrCancelled},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			menu := Menu{Output: io.Discard, ReadKey: keys(c.keys...)}

			// Act
			got, err := menu.Ask(Step{Title: "Your CFO is running", Choices: choices})

			// Assert
			if got != c.want || !errors.Is(err, c.err) {
				t.Errorf("Ask = %d, %v; want %d, %v", got, err, c.want, c.err)
			}
		})
	}
}

// Every screen says how to go on: its title, its choices with the default
// marked, Enter to continue, and the letter of a choice that has one.
func TestAMenuShowsItsDefaultAndTheKeysThatAnswerIt(t *testing.T) {
	// Arrange
	var output bytes.Buffer
	menu := Menu{Output: &output, ReadKey: keys(KeyEnter)}

	// Act
	_, err := menu.Ask(Step{Title: "Your CFO is running", Detail: "Home  C:\\CodeGoblins", Choices: []Choice{{Label: "Open the CFO terminal"}, {Label: "Open the board", Key: 'b'}}})

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	shown := output.String()
	for _, want := range []string{"Your CFO is running", "Home  C:\\CodeGoblins", "  > Open the CFO terminal", "    Open the board", "Press Enter to continue   B: Open the board   Up/Down to choose   Esc to go back"} {
		if !strings.Contains(shown, want) {
			t.Errorf("the screen lacks %q:\n%q", want, shown)
		}
	}
}

// A menu with no choices, or a default that is none of them, is refused
// before anything is read.
func TestAMenuRefusesADefaultThatIsNoChoice(t *testing.T) {
	for name, selected := range map[string]int{"below": -1, "beyond": 2} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			menu := Menu{Output: io.Discard, ReadKey: func() (Key, error) {
				t.Error("read a key for a menu it refused")
				return 0, io.EOF
			}}

			// Act
			_, err := menu.Ask(Step{Title: "Title", Choices: Labels("One", "Two"), Selected: selected})

			// Assert
			if err == nil || errors.Is(err, ErrCancelled) {
				t.Errorf("Ask error = %v, want the default refused", err)
			}
		})
	}
}

// A row wider than the console wraps onto more lines, and a redraw climbs
// back over every one of them, so the choices are drawn again in place rather
// than below the last ones.
func TestAMenuRedrawClimbsBackOverWrappedRows(t *testing.T) {
	// The hint is 60 columns; the rows are "    One" and the 34 columns of
	// four spaces and 30 x's.
	for _, c := range []struct {
		name  string
		width int
		want  string
	}{
		{"a console 20 columns wide", 20, "\x1b[7A"},
		{"a console wide enough for every row", 120, "\x1b[4A"},
		{"a console of no known width", 0, "\x1b[4A"},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			var output bytes.Buffer
			menu := Menu{Output: &output, ReadKey: keys(KeyDown, KeyEnter), Width: func() int { return c.width }}

			// Act
			_, err := menu.Ask(Step{Title: "Title", Choices: Labels("One", strings.Repeat("x", 30))})

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if moves := strings.Count(output.String(), c.want); moves != 1 {
				t.Errorf("the redraw moved up with %q %d times in %q, want once", c.want, moves, output.String())
			}
		})
	}
}

// A step answered, with Enter or with Escape, takes its whole screen with it:
// the blank line, the title, the lines under it, the choices and the hint are
// climbed back over, wrapped rows counted, and erased, so the next thing is
// drawn where the step began. Input that ended leaves the screen as it was.
func TestAStepAnsweredErasesItsScreen(t *testing.T) {
	// The step is a blank line, the title, two lines under it and a blank
	// line, then two choices, a blank line and the hint: nine lines, and
	// eleven where the 30 x's and the hint wrap in 40 columns.
	for _, c := range []struct {
		name  string
		keys  []Key
		width int
		want  string
	}{
		{"accepted", []Key{KeyEnter}, 0, "\x1b[9A\r\x1b[J"},
		{"accepted by its letter", []Key{'t'}, 0, "\x1b[9A\r\x1b[J"},
		{"gone back from", []Key{KeyEscape}, 0, "\x1b[9A\r\x1b[J"},
		{"accepted in a narrow console", []Key{KeyEnter}, 40, "\x1b[10A\r\x1b[J"},
		{"left when the input ended", nil, 0, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			var output bytes.Buffer
			menu := Menu{Output: &output, ReadKey: keys(c.keys...), Width: func() int { return c.width }}

			// Act
			_, _ = menu.Ask(Step{Title: "Title", Detail: "One line\nAnother", Choices: []Choice{{Label: "One"}, {Label: "Two", Key: 't'}}})

			// Assert
			shown := output.String()
			if c.want == "" {
				if strings.Contains(shown, "\x1b[J") {
					t.Errorf("the screen was erased though the input ended: %q", shown)
				}
				return
			}
			if !strings.HasSuffix(shown, c.want) {
				t.Errorf("the screen ends %q, want it erased with %q", shown[max(0, len(shown)-40):], c.want)
			}
		})
	}
}

// A row's escape sequences take no columns. The last step's link to the board
// is wrapped in an OSC 8 hyperlink, which makes its row 86 runes and 51
// columns: in a console between the two it is one line, for the redraw and
// for the erase, which otherwise takes the checklist's line above the step.
func TestAStepCountsALinkedRowByTheColumnsItTakes(t *testing.T) {
	const board = "http://127.0.0.1:4310"
	for _, c := range []struct {
		name string
		link string
	}{
		{"ended by ESC backslash", "\x1b]8;;" + board + "\x1b\\" + board + "\x1b]8;;\x1b\\"},
		{"ended by BEL", "\x1b]8;;" + board + "\a" + board + "\x1b]8;;\a"},
		{"in colour too", "\x1b]8;;" + board + "\x1b\\" + menuAccent + board + menuReset + "\x1b]8;;\x1b\\"},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			var output bytes.Buffer
			menu := Menu{Output: &output, ReadKey: keys(KeyDown, KeyEnter), Width: func() int { return 80 }}

			// Act
			_, err := menu.Ask(Step{
				Title:   "Your CFO is running",
				Detail:  "Home   C:\\CodeGoblins\nBoard  " + c.link + "  (Ctrl+click opens it)",
				Choices: []Choice{{Label: "Open the CFO terminal"}, {Label: "Open the board", Key: 'b'}},
			})

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			// A blank line, the title, two lines under it and a blank line, then
			// two choices, a blank line and the hint, which is 80 columns.
			shown := output.String()
			if moves := strings.Count(shown, "\x1b[4A"); moves != 1 {
				t.Errorf("the redraw moved up four lines %d times in %q, want once", moves, shown)
			}
			if !strings.HasSuffix(shown, "\x1b[9A\r\x1b[J") {
				t.Errorf("the screen ends %q, want its nine lines erased", shown[max(0, len(shown)-40):])
			}
		})
	}
}

// With NO_COLOR a step is drawn, redrawn in place and erased as it is with
// colour, and no colour sequence is written: the marked choice still shows by
// its > or its brackets.
func TestAMenuWithoutColourStillRedrawsAndErases(t *testing.T) {
	for _, c := range []struct {
		name string
		step Step
		want []string
	}{
		{"rows", Step{Title: "Title", Choices: Labels("One", "Two")}, []string{"\r\x1b[2K\nTitle\n", "\x1b[2K    One\n\x1b[2K  > Two\n", "\x1b[4A", "\x1b[7A\r\x1b[J"}},
		{"tabs", Step{Title: "Title", Tabs: true, Choices: []Choice{{Label: "One", Note: "Ready"}, {Label: "Two", Note: "Not installed"}}}, []string{"\r\x1b[2K\nTitle\n", "\x1b[2K   One   [ Two ]\n\x1b[2K   Not installed\n", "\x1b[4A", "\x1b[7A\r\x1b[J"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			var output bytes.Buffer
			menu := Menu{Output: &output, ReadKey: keys(KeyDown, KeyEnter), NoColor: true}

			// Act
			_, err := menu.Ask(c.step)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			shown := output.String()
			for _, want := range c.want {
				if !strings.Contains(shown, want) {
					t.Errorf("the screen lacks %q:\n%q", want, shown)
				}
			}
			for _, colour := range []string{menuAccent, menuMarked, menuReset} {
				if strings.Contains(shown, colour) {
					t.Errorf("the screen carries the colour sequence %q: %q", colour, shown)
				}
			}
		})
	}
}

// A row of tabs is one line: every choice with its mark, the marked one in
// brackets, and under it the marked choice's note. Left and Right move the
// mark and wrap, and the hint names those keys.
func TestARowOfTabsMovesWithLeftAndRightAndShowsTheMarkedNote(t *testing.T) {
	tabs := []Choice{{Label: "Claude Code (recommended)", Mark: "*", Note: "Ready"}, {Label: "Codex", Mark: ">_", Note: "Sign-in needed"}, {Label: "pi", Note: "Not installed"}}
	for _, c := range []struct {
		name string
		keys []Key
		want int
		row  string
		note string
	}{
		{"as first shown", []Key{KeyEnter}, 0, " [ * Claude Code (recommended) ]   >_ Codex     pi  ", "   Ready"},
		{"Right", []Key{KeyRight, KeyEnter}, 1, "   * Claude Code (recommended)   [ >_ Codex ]   pi  ", "   Sign-in needed"},
		{"Left wraps to the last", []Key{KeyLeft, KeyEnter}, 2, "   * Claude Code (recommended)     >_ Codex   [ pi ]", "   Not installed"},
		{"Down moves as Right does", []Key{KeyDown, KeyDown, KeyDown, KeyEnter}, 0, " [ * Claude Code (recommended) ]   >_ Codex     pi  ", "   Ready"},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			var output bytes.Buffer
			menu := Menu{Output: &output, ReadKey: keys(c.keys...)}

			// Act
			got, err := menu.Ask(Step{Title: "Choose the agent your CFO runs on", Choices: tabs, Tabs: true})

			// Assert
			if err != nil || got != c.want {
				t.Fatalf("Ask = %d, %v; want %d", got, err, c.want)
			}
			shown := strings.NewReplacer(menuMarked, "", menuReset, "", menuAccent, "", "\x1b[2K", "").Replace(output.String())
			// A redraw follows a move up the screen and the first draw a line
			// end, so the row is told by what follows it.
			if drawn := shown[:strings.LastIndex(shown, "\n\nPress Enter")]; !strings.HasSuffix(drawn, c.row+"\n"+c.note) {
				t.Errorf("the tabs as last drawn are %q, want %q above %q", drawn, c.row, c.note)
			}
			if !strings.Contains(shown, "Press Enter to continue   Left/Right to choose   Esc to go back") {
				t.Errorf("the hint does not name Left and Right: %q", shown)
			}
		})
	}
}

// A step with one choice has nothing to choose between, and its hint does
// not say otherwise.
func TestAStepWithOneChoiceNamesNoKeysToChooseWith(t *testing.T) {
	// Arrange
	var output bytes.Buffer
	menu := Menu{Output: &output, ReadKey: keys(KeyEnter)}

	// Act
	_, err := menu.Ask(Step{Title: "That did not finish", Detail: "exit status 1", Choices: Labels("Go back")})

	// Assert
	if err != nil || !strings.Contains(output.String(), "Press Enter to continue   Esc to go back") || strings.Contains(output.String(), "to choose") {
		t.Errorf("Ask = %v with %q, want a hint with Enter and Esc alone", err, output.String())
	}
}

// The checklist prints each finished step as one line, a tick, the step's
// name and its answer in one column, replaces the line that said what was
// under way, and takes back the lines since the last Keep, wrapped rows
// counted.
func TestTheChecklistPrintsOneLineAStepAndTakesThemBack(t *testing.T) {
	// Arrange
	var output bytes.Buffer
	list := &Checklist{Output: &output, Tick: "v", Width: func() int { return 30 }}

	// Act
	list.Done("Supervisor", "started")
	list.Keep()
	list.Working("Agent", "checking the agents on this machine")
	list.Done("Agent", "Claude Code")
	list.Note("It keeps its harness, in a note wider than the console")
	list.Clear()
	list.Clear()

	// Assert
	shown := strings.NewReplacer(menuAccent, "", menuReset, "").Replace(output.String())
	want := "\r\x1b[2K  v Supervisor started\n" +
		"\r\x1b[2K  . Agent      checking the agents on this machine" +
		"\r\x1b[2K  v Agent      Claude Code\n" +
		"\r\x1b[2K               It keeps its harness, in a note wider than the console\n" +
		// The agent's line is one row, and its note three in 30 columns; the
		// supervisor's line was kept. A second Clear has nothing to take.
		"\x1b[4A\r\x1b[J"
	if shown != want {
		t.Errorf("the checklist printed\n%q\nwant\n%q", shown, want)
	}
}

// With NO_COLOR the checklist replaces and takes back its lines as it does
// with colour, and the tick carries none.
func TestTheChecklistWithoutColourStillReplacesAndTakesBackItsLines(t *testing.T) {
	// Arrange
	var output bytes.Buffer
	list := &Checklist{Output: &output, Tick: "v", NoColor: true}

	// Act
	list.Working("Agent", "checking the agents on this machine")
	list.Done("Agent", "Claude Code")
	list.Clear()

	// Assert
	want := "\r\x1b[2K  . Agent      checking the agents on this machine" +
		"\r\x1b[2K  v Agent      Claude Code\n" +
		"\x1b[1A\r\x1b[J"
	if output.String() != want {
		t.Errorf("the checklist printed\n%q\nwant\n%q", output.String(), want)
	}
}

// A working line holds its row open for the line that replaces it. End ends
// that row once, so an error printed next starts on a row of its own; with no
// row open, and in plain output whose line was whole already, it prints
// nothing.
func TestTheChecklistEndsTheRowAWorkingLineLeftOpen(t *testing.T) {
	const working = "  . CFO        starting as Claude Code"
	for _, c := range []struct {
		name  string
		plain bool
		act   func(list *Checklist)
		want  string
	}{
		{"after a working line", false, func(list *Checklist) { list.Working("CFO", "starting as Claude Code"); list.End(); list.End() }, "\r\x1b[2K" + working + "\n"},
		{"after a finished line", false, func(list *Checklist) {
			list.Working("CFO", "starting as Claude Code")
			list.Done("CFO", "started")
			list.End()
		}, "\r\x1b[2K" + working + "\r\x1b[2K  v CFO        started\n"},
		{"after a note", false, func(list *Checklist) {
			list.Working("CFO", "starting as Claude Code")
			list.Note("A note")
			list.End()
		}, "\r\x1b[2K" + working + "\r\x1b[2K               A note\n"},
		{"with nothing under way", false, func(list *Checklist) { list.End() }, ""},
		{"in plain output", true, func(list *Checklist) { list.Working("CFO", "starting as Claude Code"); list.End() }, working + "\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			var output bytes.Buffer
			list := &Checklist{Output: &output, Tick: "v", Plain: c.plain, NoColor: true}

			// Act
			c.act(list)

			// Assert
			if output.String() != c.want {
				t.Errorf("the checklist printed %q, want %q", output.String(), c.want)
			}
		})
	}
}

// Output that is no console, such as a pipe or a log, gets every line whole,
// in order, with nothing replaced or erased.
func TestAPlainChecklistErasesNothing(t *testing.T) {
	// Arrange
	var output bytes.Buffer
	list := &Checklist{Output: &output, Tick: "v", Plain: true}

	// Act
	list.Working("Agent", "checking the agents on this machine")
	list.Done("Agent", "Claude Code")
	list.Note("A note")
	list.Clear()

	// Assert
	want := "  . Agent      checking the agents on this machine\n  v Agent      Claude Code\n               A note\n"
	if output.String() != want {
		t.Errorf("the checklist printed %q, want %q", output.String(), want)
	}
}

// The quick start draws a tick and each agent's own mark where the console
// announces Unicode, and marks every Windows console font has elsewhere.
func TestTheMarksFollowWhatTheConsoleAnnounces(t *testing.T) {
	for _, c := range []struct {
		name    string
		env     map[string]string
		unicode bool
	}{
		{"Windows Terminal", map[string]string{"WT_SESSION": "a1b2"}, true},
		{"VS Code's terminal", map[string]string{"TERM_PROGRAM": "vscode"}, true},
		{"an xterm", map[string]string{"TERM": "xterm-256color"}, true},
		{"a plain console", map[string]string{}, false},
		{"another program's terminal", map[string]string{"TERM_PROGRAM": "other", "TERM": "dumb"}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Act
			unicode := DrawsUnicode(func(name string) string { return c.env[name] })
			marks := MarksFor(unicode)

			// Assert
			if unicode != c.unicode {
				t.Fatalf("DrawsUnicode = %v, want %v", unicode, c.unicode)
			}
			tick, claude := "√", "*"
			if c.unicode {
				tick, claude = "✓", "✻"
			}
			if marks.Tick != tick || marks.Agents["claude"] != claude || marks.Agents["codex"] != ">_" {
				t.Errorf("marks = %+v, want the tick %q and Claude Code's mark %q", marks, tick, claude)
			}
		})
	}
}
