package tickets

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	issueURL   = regexp.MustCompile(`https://github\.com/([\w.-]+/[\w.-]+)/issues/(\d+)`)
	issueWords = regexp.MustCompile(`(?i)\bissues?\s+#(\d+)\b`)
)

// ClaimedIssue names the issue a task is for, when its brief or backlog row
// names exactly one: by its URL in the repository, or as "issue #N". A bare
// #N is not enough, because briefs use it for pull requests. Only the brief's
// Task and Acceptance criteria are read, as for its area, so an issue its
// Constraints mention is never claimed.
func ClaimedIssue(text, repository string) (int, bool) {
	scope := briefScope(text)
	named := map[int]bool{}
	for _, match := range issueURL.FindAllStringSubmatch(scope, -1) {
		if strings.EqualFold(match[1], repository) {
			if number, err := strconv.Atoi(match[2]); err == nil {
				named[number] = true
			}
		}
	}
	for _, match := range issueWords.FindAllStringSubmatch(scope, -1) {
		if number, err := strconv.Atoi(match[1]); err == nil {
			named[number] = true
		}
	}
	if len(named) != 1 {
		return 0, false
	}
	for number := range named {
		return number, true
	}
	return 0, false
}
