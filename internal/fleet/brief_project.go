package fleet

import "strings"

// BriefProject is the checkout a brief's Project section names, given the
// brief's lines, without a trailing parenthetical note, or empty when it names
// none.
func BriefProject(lines []string) string {
	for i, line := range lines {
		if strings.TrimSpace(line) != "## Project" {
			continue
		}
		for _, next := range lines[i+1:] {
			if next = strings.TrimSpace(next); next != "" {
				if strings.HasPrefix(next, "#") {
					return ""
				}
				checkout, _, _ := strings.Cut(next, " (")
				return strings.TrimSpace(checkout)
			}
		}
	}
	return ""
}
