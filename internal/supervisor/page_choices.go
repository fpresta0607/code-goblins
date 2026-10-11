package supervisor

import (
	"bytes"
	"encoding/json"
	"regexp"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// choicesBlock is a data-lavish-choices block of a Scrawl page: the question
// the page asks the Overlord to pick an answer to, declared as data, which
// Scrawl draws as a radio list (the lavish skill's Choices).
var choicesBlock = regexp.MustCompile(`(?is)<script\b[^>]*\bdata-lavish-choices\b[^>]*>(.*?)</script\s*>`)

// declaredQuestion is one question a choices block declares: an object, or
// one of a list of them.
type declaredQuestion struct {
	Options     []string `json:"options"`
	Recommended string   `json:"recommended"`
}

// PageOptions is what a goblin's Scrawl page asks the Overlord to pick, as a
// notify offers it: " options: " and the choices of the one question the
// page's choices blocks declare, the recommended one marked "(Recommended)"
// as a goblin marks it. While AFK mode is on the CFO answers the page in his
// place with one of them. It is empty for a page that declares no question,
// several, or choices a question cannot offer, which the CFO answers in its
// own words, and for a page that cannot be read.
func PageOptions(page string) string {
	data, err := fsx.ReadFile(page)
	if err != nil {
		return ""
	}
	var declared []declaredQuestion
	for _, block := range choicesBlock.FindAllSubmatch(data, -1) {
		body := bytes.TrimSpace(block[1])
		var list []declaredQuestion
		if json.Unmarshal(body, &list) == nil {
			declared = append(declared, list...)
			continue
		}
		var one declaredQuestion
		if json.Unmarshal(body, &one) != nil {
			return ""
		}
		declared = append(declared, one)
	}
	if len(declared) != 1 {
		return ""
	}
	asked := declared[0]
	if len(asked.Options) == 0 || len(asked.Options) > 8 || asked.Recommended != "" && !slices.Contains(asked.Options, asked.Recommended) {
		return ""
	}
	marked := make([]string, len(asked.Options))
	for i, option := range asked.Options {
		// A choice is bounded as validQuestion bounds one, and a notify lists
		// its choices after one "options:" marker, split by "|", the
		// recommended one ending "(Recommended)".
		if strings.TrimSpace(option) == "" || len(option) > 500 || slices.Contains(asked.Options[:i], option) ||
			strings.ContainsAny(option, "|\r\n") || strings.Contains(option, "options:") || strings.HasSuffix(option, "(Recommended)") {
			return ""
		}
		marked[i] = option
		if option == asked.Recommended {
			marked[i] += " (Recommended)"
		}
	}
	return " options: " + strings.Join(marked, " | ")
}
