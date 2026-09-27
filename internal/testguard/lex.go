package testguard

import (
	"path"
	"strings"
	"unicode"
)

// codeLines is content split into lines with the inside of every string and
// comment literal blanked, so a skip marker is judged by the code tokens
// alone: t.Skip written in a test's own code counts, while the same text in a
// string, such as the fixture a checker's own tests describe a skip with, or
// in a comment does not. Strings and comments can span lines (Go raw strings
// and block comments, Python triple quotes, JavaScript templates, PowerShell
// here-strings), so the whole file is read in one pass and the line breaks are
// kept, leaving every line at its own number. A file in a language this does
// not know keeps every line as it is, which can only count more.
func codeLines(file, content string) []string {
	var blanked string
	switch strings.ToLower(path.Ext(file)) {
	case ".go":
		blanked = lexGo(content)
	case ".py":
		blanked = lexPython(content)
	case ".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs", ".mts", ".cts":
		blanked = lexJS(content)
	case ".ps1", ".psm1":
		blanked = lexPowerShell(content)
	default:
		blanked = content
	}
	return strings.Split(blanked, "\n")
}

// blanker builds the blanked copy of a file: code is copied as it is, and the
// inside of a literal becomes spaces, except for its line breaks.
type blanker struct {
	src []rune
	out []rune
	i   int
}

func newBlanker(content string) *blanker {
	src := []rune(content)
	return &blanker{src: src, out: make([]rune, 0, len(src))}
}

func (b *blanker) done() bool { return b.i >= len(b.src) }

// at reports whether the source continues with s at the current position.
func (b *blanker) at(s string) bool {
	r := []rune(s)
	if b.i+len(r) > len(b.src) {
		return false
	}
	for k, c := range r {
		if b.src[b.i+k] != c {
			return false
		}
	}
	return true
}

// code copies n runes as code.
func (b *blanker) code(n int) {
	for k := 0; k < n && !b.done(); k++ {
		b.out = append(b.out, b.src[b.i])
		b.i++
	}
}

// blank copies n runes as the inside of a literal.
func (b *blanker) blank(n int) {
	for k := 0; k < n && !b.done(); k++ {
		if b.src[b.i] == '\n' || b.src[b.i] == '\r' {
			b.out = append(b.out, b.src[b.i])
		} else {
			b.out = append(b.out, ' ')
		}
		b.i++
	}
}

// blankLine blanks to the end of the line, the line break kept as code.
func (b *blanker) blankLine() {
	for !b.done() && b.src[b.i] != '\n' && b.src[b.i] != '\r' {
		b.blank(1)
	}
}

// blankUntil blanks up to and including close, across lines; escape, when
// not zero, blanks the rune after it too, so an escaped close is skipped.
// With stopAtLine the literal also ends at a line break, as an unterminated
// quoted string does.
func (b *blanker) blankUntil(close string, escape rune, stopAtLine bool) {
	for !b.done() {
		if stopAtLine && (b.src[b.i] == '\n' || b.src[b.i] == '\r') {
			return
		}
		if escape != 0 && b.src[b.i] == escape {
			b.blank(2)
			continue
		}
		if b.at(close) {
			b.code(len([]rune(close)))
			return
		}
		b.blank(1)
	}
}

func lexGo(content string) string {
	b := newBlanker(content)
	for !b.done() {
		switch {
		case b.at("//"):
			b.code(2)
			b.blankLine()
		case b.at("/*"):
			b.code(2)
			b.blankUntil("*/", 0, false)
		case b.at(`"`):
			b.code(1)
			b.blankUntil(`"`, '\\', true)
		case b.at("`"):
			b.code(1)
			b.blankUntil("`", 0, false)
		case b.at("'"):
			b.code(1)
			b.blankUntil("'", '\\', true)
		default:
			b.code(1)
		}
	}
	return string(b.out)
}

func lexPython(content string) string {
	b := newBlanker(content)
	for !b.done() {
		switch {
		case b.at("#"):
			b.code(1)
			b.blankLine()
		case b.at(`"""`) || b.at("'''"):
			quote := string(b.src[b.i : b.i+3])
			b.code(3)
			b.blankUntil(quote, '\\', false)
		case b.at(`"`) || b.at("'"):
			quote := string(b.src[b.i])
			b.code(1)
			b.blankUntil(quote, '\\', true)
		default:
			b.code(1)
		}
	}
	return string(b.out)
}

// lexJS blanks JavaScript and TypeScript literals. A template's ${...} is
// code, so a marker written there counts, and templates nest inside it.
func lexJS(content string) string {
	b := newBlanker(content)
	var depths []int
	for !b.done() {
		switch {
		case b.at("//"):
			b.code(2)
			b.blankLine()
		case b.at("/*"):
			b.code(2)
			b.blankUntil("*/", 0, false)
		case b.at("/") && jsRegexStarts(b.out):
			jsRegex(b)
		case b.at(`"`) || b.at("'"):
			quote := string(b.src[b.i])
			b.code(1)
			b.blankUntil(quote, '\\', true)
		case b.at("`"):
			b.code(1)
			if jsTemplate(b) {
				depths = append(depths, 0)
			}
		case len(depths) > 0 && b.at("{"):
			depths[len(depths)-1]++
			b.code(1)
		case len(depths) > 0 && b.at("}"):
			b.code(1)
			if depths[len(depths)-1] > 0 {
				depths[len(depths)-1]--
				continue
			}
			depths = depths[:len(depths)-1]
			if jsTemplate(b) {
				depths = append(depths, 0)
			}
		default:
			b.code(1)
		}
	}
	return string(b.out)
}

// jsTemplate blanks a template's text up to its closing backtick, or up to a
// ${, which it keeps as code and reports, so the caller reads the expression.
func jsTemplate(b *blanker) bool {
	for !b.done() {
		switch {
		case b.at(`\`):
			b.blank(2)
		case b.at("`"):
			b.code(1)
			return false
		case b.at("${"):
			b.code(2)
			return true
		default:
			b.blank(1)
		}
	}
	return false
}

// jsRegexKeywords are the words after which a slash starts a regular
// expression rather than a division.
var jsRegexKeywords = map[string]bool{
	"return": true, "typeof": true, "case": true, "do": true, "else": true, "in": true, "of": true,
	"new": true, "delete": true, "void": true, "throw": true, "yield": true, "await": true,
}

// jsRegexStarts reports whether a slash after the code blanked so far starts
// a regular expression: at the start of the file, after an operator or an
// opening bracket, or after a keyword; after a name, a number or a closing
// bracket it is a division.
func jsRegexStarts(out []rune) bool {
	end := len(out)
	for end > 0 && unicode.IsSpace(out[end-1]) {
		end--
	}
	if end == 0 || strings.ContainsRune("(,=:[!&|?{};+-*%<>~^", out[end-1]) {
		return true
	}
	start := end
	for start > 0 && (unicode.IsLetter(out[start-1]) || unicode.IsDigit(out[start-1]) || out[start-1] == '_' || out[start-1] == '$') {
		start--
	}
	return jsRegexKeywords[string(out[start:end])]
}

// jsRegex blanks a regular expression literal up to its closing slash, which
// a slash inside a [...] class or after a backslash is not; its flags follow
// as code. It never crosses a line break, so a division read as a regular
// expression blanks at most the rest of its line.
func jsRegex(b *blanker) {
	b.code(1)
	inClass, escaped := false, false
	for !b.done() && b.src[b.i] != '\n' && b.src[b.i] != '\r' {
		switch c := b.src[b.i]; {
		case escaped:
			escaped = false
		case c == '\\':
			escaped = true
		case c == '[':
			inClass = true
		case c == ']':
			inClass = false
		case c == '/' && !inClass:
			b.code(1)
			return
		}
		b.blank(1)
	}
}

func lexPowerShell(content string) string {
	b := newBlanker(content)
	for !b.done() {
		switch {
		case b.at("<#"):
			b.code(2)
			b.blankUntil("#>", 0, false)
		case b.at("#"):
			b.code(1)
			b.blankLine()
		case b.at("@'") || b.at(`@"`):
			quote := string(b.src[b.i+1])
			b.code(2)
			b.blankUntil("\n"+quote+"@", 0, false)
		case b.at("'"):
			b.code(1)
			for !b.done() {
				if b.at("''") {
					b.blank(2)
					continue
				}
				if b.at("'") {
					b.code(1)
					break
				}
				b.blank(1)
			}
		case b.at(`"`):
			b.code(1)
			for !b.done() {
				if b.at("`") || b.at(`""`) {
					b.blank(2)
					continue
				}
				if b.at(`"`) {
					b.code(1)
					break
				}
				b.blank(1)
			}
		default:
			b.code(1)
		}
	}
	return string(b.out)
}
