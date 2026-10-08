package main

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// How much of a message a notification says, and of the asker's name it
// starts with, as the board's page cuts them, so a goblin titled with a whole
// sentence still leaves room for what it asks.
const (
	textLimit = 160
	nameLimit = 60
)

// The little structure a message may carry, as the board's page reads it:
// **bold** and `code` marks, "- " bullets and pipe tables, with paragraphs
// apart by a blank line.
var (
	harnessSuffix  = regexp.MustCompile(`(?i);\s*(?:Claude Code|Codex|Pi|Kimi)\s*$`)
	boldMark       = regexp.MustCompile(`\*\*(\S(?:[\s\S]*?\S)?)\*\*`)
	codeMark       = regexp.MustCompile("`[^`\n]+`")
	tableRow       = regexp.MustCompile(`^\|.*\|$`)
	tableSeparator = regexp.MustCompile(`^\|(\s*:?-{3,}:?\s*\|)+$`)
	blankLine      = regexp.MustCompile(`\n\s*\n`)
	waitPrefix     = regexp.MustCompile(`^Waiting on you:\s*`)
)

// block is a paragraph's lines, or a list's items.
type block struct {
	isList bool
	parts  []string
}

// blocks reads a message's paragraphs and lists, leaving its tables out,
// since their values only read on the item's card.
func blocks(text string) []block {
	var out []block
	for _, part := range blankLine.Split(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n"), -1) {
		var lines []string
		for _, line := range strings.Split(part, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				lines = append(lines, line)
			}
		}
		var prose, items []string
		flush := func() {
			if len(prose) > 0 {
				out = append(out, block{parts: []string{strings.Join(prose, "\n")}})
			}
			if len(items) > 0 {
				out = append(out, block{isList: true, parts: items})
			}
			prose, items = nil, nil
		}
		for n := 0; n < len(lines); n++ {
			line := lines[n]
			switch {
			case tableRow.MatchString(line) && n+1 < len(lines) && tableSeparator.MatchString(lines[n+1]):
				flush()
				for n += 2; n < len(lines) && tableRow.MatchString(lines[n]); n++ {
				}
				n--
			case strings.HasPrefix(line, "- "):
				if len(prose) > 0 {
					out = append(out, block{parts: []string{strings.Join(prose, "\n")}})
					prose = nil
				}
				items = append(items, strings.TrimSpace(line[2:]))
			default:
				if len(items) > 0 {
					out = append(out, block{isList: true, parts: items})
					items = nil
				}
				prose = append(prose, line)
			}
		}
		flush()
	}
	return out
}

// unmarked is text without its bold and code marks. A code value is taken as
// written, and bold is looked for with each code value covered, so marks
// inside a value stay its own and a bold phrase can run across one.
func unmarked(text string) string {
	drop := map[int]bool{}
	codes := codeMark.FindAllStringIndex(text, -1)
	covered := []byte(text)
	for _, value := range codes {
		drop[value[0]], drop[value[1]-1] = true, true
		for i := value[0]; i < value[1]; i++ {
			covered[i] = 'x'
		}
	}
	for _, phrase := range boldMark.FindAllIndex(covered, -1) {
		drop[phrase[0]], drop[phrase[0]+1], drop[phrase[1]-2], drop[phrase[1]-1] = true, true, true, true
	}
	var out strings.Builder
	for i := 0; i < len(text); i++ {
		if !drop[i] {
			out.WriteByte(text[i])
		}
	}
	return out.String()
}

// oneLine is text with its runs of white space, line breaks among them, as
// one space.
func oneLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

// lead is what a question asks first: its first paragraph or bullet, on one
// line, without the details or tables of values around it.
func lead(text string) string {
	if found := blocks(text); len(found) > 0 {
		return oneLine(unmarked(found[0].parts[0]))
	}
	return ""
}

// plain is a message on one line: marks dropped, a list's items joined, and
// a table left out.
func plain(text string) string {
	var said []string
	for _, b := range blocks(text) {
		if b.isList {
			items := make([]string, len(b.parts))
			for i, item := range b.parts {
				items[i] = unmarked(item)
			}
			said = append(said, strings.Join(items, "; "))
			continue
		}
		said = append(said, oneLine(unmarked(b.parts[0])))
	}
	return strings.Join(said, " ")
}

// shortened is text cut to limit characters, ending with an ellipsis when it
// was cut.
func shortened(text string, limit int) string {
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	return strings.TrimRight(string([]rune(text)[:limit-1]), " \t\n") + "…"
}

// withoutHarness is a goblin's title without the harness its card names.
func withoutHarness(title string) string {
	return strings.TrimSpace(harnessSuffix.ReplaceAllString(title, ""))
}
