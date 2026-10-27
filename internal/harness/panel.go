package harness

import (
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Panel is what a harness draws in its input line's place while something of
// its own is open, a question, a permission prompt, a picker or a panel, read
// so the board can offer it as a list he picks from: the rows that ask or
// show, the choices it lists, and the keys its footer names.
type Panel struct {
	Rows    []string `json:"rows,omitempty"`
	Choices []Choice `json:"choices,omitempty"`
	Hints   []string `json:"hints,omitempty"`
}

// Choice is one choice a panel lists: its words, its number when the harness
// numbers them, and whether the harness's focus is on it.
type Choice struct {
	Text    string `json:"text"`
	Number  int    `json:"number,omitempty"`
	Focused bool   `json:"focused,omitempty"`
}

// panelRowLimit is how many of the screen's last rows a panel can take, so a
// long preview above the choices keeps its newest rows and never the
// conversation's.
const panelRowLimit = 30

var (
	// numberedChoice is a numbered choice's row, its focus mark first when it
	// has the focus: ❯ or > for Claude Code, › for Codex and → for pi.
	numberedChoice = regexp.MustCompile(`^(\s*)(?:([❯>›→])\s*)?(\d+)\.\s+(\S.*)$`)
	// focusedRow is the row an unnumbered list marks as focused.
	focusedRow = regexp.MustCompile(`^(\s*)([❯>›→])\s+(\S.*)$`)
	// keyHint is a footer row naming the keys a panel takes.
	keyHint = regexp.MustCompile(`(?i)\b(esc|escape|enter|tab)\b|↑|↓|ctrl\+`)
)

// ReadPanel reads screen, a harness's screen with something of its own open,
// as a Panel. The panel is the screen's last rows below the conversation: up
// to the last full-width rule, the last row of a message the conversation
// draws (● for Claude Code, • for Codex) or panelRowLimit rows.
func ReadPanel(screen []string) Panel {
	region := panelRegion(screen)
	start, choices, end := numberedChoices(region)
	if choices == nil {
		start, choices, end = markedChoices(region)
	}
	if choices == nil {
		hints := len(region)
		for hints > 0 && keyHint.MatchString(region[hints-1]) {
			hints--
		}
		start, end = hints, hints
	}
	return Panel{Rows: plainRows(region[:start]), Choices: choices, Hints: hintRows(region[end:])}
}

// panelRegion returns the rows the panel takes, without blank rows at either
// end.
func panelRegion(screen []string) []string {
	end := len(screen) - emptyRowsAtEnd(screen)
	start := end
	for start > 0 && end-start < panelRowLimit {
		row := strings.TrimSpace(screen[start-1])
		if isRule(screen[start-1]) || strings.HasPrefix(row, "●") || strings.HasPrefix(row, "•") || strings.HasPrefix(row, "⎿") {
			break
		}
		start--
	}
	for start < end && strings.TrimSpace(screen[start]) == "" {
		start++
	}
	return screen[start:end]
}

// numberedChoices reads the list of numbered choices the region ends with,
// from its last choice numbered 1: each choice with the rows its words wrap
// onto, which lie further in than its number. It returns where the list
// starts and ends in the region, and no choices when the region has none.
func numberedChoices(region []string) (int, []Choice, int) {
	start := -1
	for at, row := range region {
		if match := numberedChoice.FindStringSubmatch(row); match != nil && match[3] == "1" {
			start = at
		}
	}
	if start < 0 {
		return 0, nil, 0
	}
	var choices []Choice
	column, end := 0, start
	for ; end < len(region); end++ {
		row := region[end]
		if match := numberedChoice.FindStringSubmatch(row); match != nil {
			number, _ := strconv.Atoi(match[3])
			column = utf8.RuneCountInString(row[:strings.Index(row, match[3])])
			choices = append(choices, Choice{Text: strings.TrimSpace(match[4]), Number: number, Focused: match[2] != ""})
			continue
		}
		if strings.TrimSpace(row) == "" || indent(row) <= column {
			break
		}
		choices[len(choices)-1].Text += " " + strings.TrimSpace(row)
	}
	return start, choices, end
}

// markedChoices reads an unnumbered list by its focused row: the rows next to
// it, with no blank row between, that line up with its mark or its words,
// each a choice, and a row in brackets the detail of the choice above it.
func markedChoices(region []string) (int, []Choice, int) {
	focused := -1
	for at, row := range region {
		if focusedRow.MatchString(row) {
			focused = at
		}
	}
	if focused < 0 {
		return 0, nil, 0
	}
	match := focusedRow.FindStringSubmatch(region[focused])
	mark, words := utf8.RuneCountInString(match[1]), strings.Index(region[focused], match[3])
	isSibling := func(row string) bool {
		return strings.TrimSpace(row) != "" && !keyHint.MatchString(row) && (indent(row) == mark || indent(row) == utf8.RuneCountInString(region[focused][:words]))
	}
	start, end := focused, focused+1
	for start > 0 && isSibling(region[start-1]) {
		start--
	}
	for end < len(region) && isSibling(region[end]) {
		end++
	}
	var choices []Choice
	for at := start; at < end; at++ {
		text := strings.TrimSpace(region[at])
		if at == focused {
			text = strings.TrimSpace(match[3])
		}
		if strings.HasPrefix(text, "(") && len(choices) > 0 {
			choices[len(choices)-1].Text += " " + text
			continue
		}
		choices = append(choices, Choice{Text: text, Focused: at == focused})
	}
	return start, choices, end
}

// plainRows returns rows without the blank rows at their end and without the
// indent they all share, a rule's dashes aside.
func plainRows(rows []string) []string {
	rows = rows[:len(rows)-emptyRowsAtEnd(rows)]
	shared := -1
	for _, row := range rows {
		if strings.TrimSpace(row) != "" && !isDivider(row) && (shared < 0 || indent(row) < shared) {
			shared = indent(row)
		}
	}
	var plain []string
	for _, row := range rows {
		row = strings.TrimRight(row, " ")
		if shared > 0 && indent(row) >= shared {
			row = string([]rune(row)[shared:])
		}
		plain = append(plain, row)
	}
	return plain
}

// hintRows returns the footer's rows that are not blank, trimmed.
func hintRows(rows []string) []string {
	var hints []string
	for _, row := range rows {
		if row = strings.TrimSpace(row); row != "" {
			hints = append(hints, row)
		}
	}
	return hints
}

// indent counts the spaces a row starts with.
func indent(row string) int {
	return utf8.RuneCountInString(row) - utf8.RuneCountInString(strings.TrimLeft(row, " "))
}

// isDivider reports whether row is a line of box-drawing dashes, solid or
// broken, as a harness draws above a preview or around a diff.
func isDivider(row string) bool {
	row = strings.TrimSpace(row)
	return row != "" && strings.Trim(row, "─╌━═") == ""
}
