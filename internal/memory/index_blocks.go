package memory

import "strings"

// Containers hold list continuation widths; zero denotes a blockquote.
// Fences end at their container boundary even without a closing delimiter.
type indexCodeBlocks struct {
	containers  []int
	fence       string
	isParagraph bool
}

func (blocks *indexCodeBlocks) isLiteral(line string) bool {
	var expanded strings.Builder
	for position := 0; position < len(line); position++ {
		if line[position] == '\t' {
			expanded.WriteString(strings.Repeat(" ", 4-expanded.Len()%4))
		} else {
			expanded.WriteByte(line[position])
		}
	}
	line = strings.TrimSuffix(expanded.String(), "\r")
	for index, width := range blocks.containers {
		if width == 0 {
			width = indexQuoteWidth(line)
		} else if strings.Trim(line, " ") == "" {
			line = ""
			continue
		} else if len(line)-len(strings.TrimLeft(line, " ")) < width {
			width = 0
		}
		if width == 0 {
			blocks.containers = blocks.containers[:index]
			blocks.fence = ""
			break
		}
		line = line[width:]
	}
	if blocks.fence == "" {
		for {
			if indexDivider(line, blocks.isParagraph) {
				break
			}
			if width := indexQuoteWidth(line); width != 0 {
				blocks.containers = append(blocks.containers, 0)
				line = line[width:]
			} else if width := indexListWidth(line, blocks.isParagraph); width != 0 {
				blocks.containers = append(blocks.containers, width)
				line = line[min(width, len(line)):]
			} else {
				break
			}
			blocks.isParagraph = false
		}
	}
	trimmed := strings.TrimLeft(line, " ")
	indent := len(line) - len(trimmed)
	if blocks.fence != "" {
		width := len(trimmed) - len(strings.TrimLeft(trimmed, blocks.fence[:1]))
		if indent <= 3 && width >= len(blocks.fence) && strings.Trim(trimmed[width:], " ") == "" {
			blocks.fence = ""
		}
		return true
	}
	if trimmed == "" {
		blocks.isParagraph = false
		return false
	}
	if indent >= 4 {
		return !blocks.isParagraph
	}
	if indexDivider(line, blocks.isParagraph) {
		blocks.isParagraph = false
		return false
	}
	if trimmed[0] == '`' || trimmed[0] == '~' {
		marker := trimmed[:1]
		width := len(trimmed) - len(strings.TrimLeft(trimmed, marker))
		if width >= 3 && (marker == "~" || !strings.Contains(trimmed[width:], "`")) {
			blocks.fence = trimmed[:width]
			blocks.isParagraph = false
			return true
		}
	}
	blocks.isParagraph = true
	if trimmed[0] == '#' {
		width := len(trimmed) - len(strings.TrimLeft(trimmed, "#"))
		blocks.isParagraph = width > 6 || width < len(trimmed) && trimmed[width] != ' '
	}
	return false
}

func indexQuoteWidth(line string) int {
	trimmed := strings.TrimLeft(line, " ")
	indent := len(line) - len(trimmed)
	if indent > 3 || !strings.HasPrefix(trimmed, ">") {
		return 0
	}
	width := indent + 1
	if width < len(line) && line[width] == ' ' {
		width++
	}
	return width
}

func indexListWidth(line string, isParagraph bool) int {
	trimmed := strings.TrimLeft(line, " ")
	indent := len(line) - len(trimmed)
	if indent > 3 || trimmed == "" {
		return 0
	}
	width := 1
	if !strings.ContainsRune("-+*", rune(trimmed[0])) {
		width = 0
		for width < len(trimmed) && trimmed[width] >= '0' && trimmed[width] <= '9' {
			width++
		}
		if width == 0 || width > 9 || width == len(trimmed) || !strings.ContainsRune(".)", rune(trimmed[width])) {
			return 0
		}
		if isParagraph && trimmed[:width] != "1" {
			return 0
		}
		width++
	}
	if width < len(trimmed) && trimmed[width] != ' ' {
		return 0
	}
	padding := len(trimmed[width:]) - len(strings.TrimLeft(trimmed[width:], " "))
	if isParagraph && width+padding == len(trimmed) {
		return 0
	}
	if padding == 0 || padding > 4 {
		padding = 1
	}
	return indent + width + padding
}

func indexDivider(line string, isParagraph bool) bool {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 || trimmed == "" {
		return false
	}
	marker := trimmed[:1]
	if strings.ContainsAny(marker, "-_*") && len(strings.ReplaceAll(trimmed, " ", "")) >= 3 && strings.Trim(trimmed, marker+" ") == "" {
		return true
	}
	return isParagraph && strings.ContainsAny(marker, "-=") && strings.Trim(strings.TrimRight(trimmed, " "), marker) == ""
}
