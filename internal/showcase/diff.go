package showcase

import (
	"fmt"
	"html"
	"strconv"
	"strings"
)

// DiffLine is one rendered line of a unified diff hunk. Kind is ' ' for
// context, '+' for an addition, and '-' for a deletion.
type DiffLine struct {
	Old    int
	New    int
	Kind   byte
	Text   string
	Anchor string
}

// DiffHunk is one @@-delimited section of a file diff.
type DiffHunk struct {
	Header string
	Lines  []DiffLine
}

// DiffFile is one file's worth of a patch.
type DiffFile struct {
	OldPath string
	NewPath string
	Hunks   []DiffHunk
}

// ParseDiff parses unified diff source into per-file hunks with old and new
// line numbers. Text before the first "diff --git" (commit headers, cover
// letters) is kept as a preamble file.
func ParseDiff(source []byte) []DiffFile {
	var files []*DiffFile
	preamble := &DiffFile{}
	current := preamble
	oldLine, newLine := 0, 0
	for _, raw := range strings.Split(strings.ReplaceAll(string(source), "\r\n", "\n"), "\n") {
		switch {
		case strings.HasPrefix(raw, "diff --git "):
			current = &DiffFile{}
			files = append(files, current)
			oldLine, newLine = 0, 0
		case strings.HasPrefix(raw, "--- "):
			current.OldPath = diffPath(raw[4:])
		case strings.HasPrefix(raw, "+++ "):
			current.NewPath = diffPath(raw[4:])
		case strings.HasPrefix(raw, "@@ "):
			oldLine, newLine = parseHunkRange(raw)
			current.Hunks = append(current.Hunks, DiffHunk{Header: raw})
		case strings.HasPrefix(raw, `\`):
			// "\ No newline at end of file" - no review value.
		case len(current.Hunks) > 0 && len(raw) > 0 && (raw[0] == '+' || raw[0] == '-' || raw[0] == ' '):
			hunk := &current.Hunks[len(current.Hunks)-1]
			line := DiffLine{Kind: raw[0], Text: raw[1:]}
			switch raw[0] {
			case '+':
				line.New = newLine
				newLine++
			case '-':
				line.Old = oldLine
				oldLine++
			default:
				line.Old = oldLine
				line.New = newLine
				oldLine++
				newLine++
			}
			hunk.Lines = append(hunk.Lines, line)
		default:
			if len(current.Hunks) == 0 && strings.TrimSpace(raw) != "" {
				// Header lines between "diff --git" and the first hunk
				// (index, similarity, mode changes) stay visible.
				current.Hunks = append(current.Hunks, DiffHunk{Lines: []DiffLine{{Kind: ' ', Text: raw}}})
			}
		}
	}
	result := make([]DiffFile, 0, len(files)+1)
	if len(preamble.Hunks) > 0 {
		result = append(result, *preamble)
	}
	for _, file := range files {
		result = append(result, *file)
	}
	return result
}

func diffPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "/dev/null" {
		return path
	}
	if strings.HasPrefix(path, "a/") || strings.HasPrefix(path, "b/") {
		return path[2:]
	}
	return path
}

func parseHunkRange(header string) (oldStart, newStart int) {
	fields := strings.Fields(header)
	oldStart, newStart = 1, 1
	for _, field := range fields {
		if strings.HasPrefix(field, "-") {
			oldStart, _ = strconv.Atoi(strings.SplitN(strings.TrimPrefix(field, "-"), ",", 2)[0])
		}
		if strings.HasPrefix(field, "+") {
			newStart, _ = strconv.Atoi(strings.SplitN(strings.TrimPrefix(field, "+"), ",", 2)[0])
		}
	}
	return oldStart, newStart
}

// RenderDiff renders parsed files as review markup: a header per file, hunk
// separators, old/new line numbers, and an anchor per line.
func RenderDiff(files []DiffFile) string {
	var out strings.Builder
	for fileIndex, file := range files {
		name := file.NewPath
		if name == "" || name == "/dev/null" {
			name = file.OldPath
		}
		if name == "" {
			name = "(preamble)"
		}
		fmt.Fprintf(&out, `<section class="diff-file" id="f%d"><header>`, fileIndex)
		out.WriteString(html.EscapeString(name))
		if file.OldPath != "" && file.NewPath != "" && file.OldPath != file.NewPath {
			out.WriteString(` <span class="renamed">(from `)
			out.WriteString(html.EscapeString(file.OldPath))
			out.WriteString(`)</span>`)
		}
		out.WriteString("</header>")
		for _, hunk := range file.Hunks {
			if hunk.Header != "" {
				out.WriteString(`<div class="hunk-header">`)
				out.WriteString(html.EscapeString(hunk.Header))
				out.WriteString("</div>")
			}
			for lineIndex, line := range hunk.Lines {
				class := "ctx"
				switch line.Kind {
				case '+':
					class = "add"
				case '-':
					class = "del"
				}
				anchor := ""
				if line.New > 0 {
					anchor = fmt.Sprintf(` id="f%d-n%d"`, fileIndex, line.New)
				} else if line.Old > 0 {
					anchor = fmt.Sprintf(` id="f%d-o%d"`, fileIndex, line.Old)
				} else {
					anchor = fmt.Sprintf(` id="f%d-x%d"`, fileIndex, lineIndex)
				}
				fmt.Fprintf(&out, `<div class="dl %s"%s><span class="no">%s</span><span class="no">%s</span><code>%s</code></div>`,
					class, anchor, lineNo(line.Old), lineNo(line.New), html.EscapeString(line.Text))
			}
		}
		out.WriteString("</section>")
	}
	return out.String()
}

func lineNo(n int) string {
	if n <= 0 {
		return ""
	}
	return strconv.Itoa(n)
}
