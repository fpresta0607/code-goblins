package tickets

import (
	"regexp"
	"strings"
)

// maxWhat is the most characters of a brief's Ticket section a ticket
// carries: two or three sentences, never the brief.
const maxWhat = 600

// markdownHeading is a heading line of any level and its text. A line such
// as "#1448 asks for this", with no space after its hashes, is not one.
var markdownHeading = regexp.MustCompile(`^(#{1,6})(?:\s+(.*))?$`)

// TicketSection is a brief's Ticket section, the two or three sentences the
// CFO wrote there for teammates, on one line and cut at a word to maxWhat
// characters, or "" when the brief has none. It is the one text of a brief a
// ticket carries. The section ends at the next heading of any level.
func TicketSection(brief string) string {
	var section []string
	inSection := false
	for _, line := range strings.Split(brief, "\n") {
		trimmed := strings.TrimSpace(line)
		if heading := markdownHeading.FindStringSubmatch(trimmed); heading != nil {
			inSection = heading[1] == "##" && strings.EqualFold(strings.TrimSpace(strings.TrimSuffix(heading[2], ":")), "ticket")
			continue
		}
		if inSection {
			section = append(section, trimmed)
		}
	}
	return cut(strings.Join(strings.Fields(strings.Join(section, " ")), " "), maxWhat)
}
