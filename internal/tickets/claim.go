package tickets

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var (
	issueURL    = regexp.MustCompile(`https://github\.com/([\w.-]+/[\w.-]+)/issues/(\d+)`)
	issueWords  = regexp.MustCompile(`(?i)\bissues?\s+#\d+(?:(?:\s*(?:,|&|\band\b|\bor\b))+\s*#\d+)*\b`)
	issueNumber = regexp.MustCompile(`#(\d+)`)
	// issueLine is a line that opens with bare issue numbers, after a list
	// marker or a heading's, as in "- #1343 Project Home ..." or
	// "#1263 and #1260 are one bug".
	issueLine = regexp.MustCompile(`^\s*(?:(?:[-*+]|\d+[.)]|#{1,6})\s+)?(#\d+(?:(?:\s*(?:,|&|\band\b|\bor\b))+\s*#\d+)*)\b`)
)

// NamedIssue is an issue a task's brief names to take up.
type NamedIssue struct {
	Number int
	// IsListed says the brief names it only by a bare #N that opens a line
	// of its Task. Briefs list pull requests that way too, so only GitHub
	// can say whether it is an issue to take up at all.
	IsListed bool
}

// NamedIssues are the issues a task's brief or backlog row names to take up:
// first those it names by URL in the repository, or as "issue #N" or
// "issues #N and #M", in its Task and Acceptance criteria, in the order it
// names them, then those a bare #N opens a line of its Task with, as a brief
// lists the issues it closes. A bare #N inside a sentence is not enough,
// because briefs use it for pull requests. An issue its Constraints mention
// is never taken up.
func NamedIssues(text, repository string) []NamedIssue {
	type mention struct {
		at     int
		number int
	}
	var mentions []mention
	scope := briefScope(text)
	for _, match := range issueURL.FindAllStringSubmatchIndex(scope, -1) {
		if strings.EqualFold(scope[match[2]:match[3]], repository) {
			if number, err := strconv.Atoi(scope[match[4]:match[5]]); err == nil {
				mentions = append(mentions, mention{match[0], number})
			}
		}
	}
	for _, words := range issueWords.FindAllStringIndex(scope, -1) {
		for _, match := range issueNumber.FindAllStringSubmatchIndex(scope[words[0]:words[1]], -1) {
			if number, err := strconv.Atoi(scope[words[0]+match[2] : words[0]+match[3]]); err == nil {
				mentions = append(mentions, mention{words[0] + match[0], number})
			}
		}
	}
	slices.SortStableFunc(mentions, func(a, b mention) int { return a.at - b.at })
	var named []NamedIssue
	add := func(issue NamedIssue) {
		if !slices.ContainsFunc(named, func(known NamedIssue) bool { return known.Number == issue.Number }) {
			named = append(named, issue)
		}
	}
	for _, m := range mentions {
		add(NamedIssue{Number: m.number})
	}
	for _, line := range strings.Split(taskSection(text), "\n") {
		if numbers := issueLine.FindStringSubmatch(line); numbers != nil {
			for _, match := range issueNumber.FindAllStringSubmatch(numbers[1], -1) {
				if number, err := strconv.Atoi(match[1]); err == nil {
					add(NamedIssue{Number: number, IsListed: true})
				}
			}
		}
	}
	return named
}

// HeldElsewhere says why an issue a task names is someone else's work, or ""
// when it is not: someone other than the account the fleet writes as is
// assigned to it, or a Code Goblins state label is on it while that account
// is not, which is how another account's fleet holds an issue it opened or
// claimed. A fleet assigns its own account to every issue it opens or claims.
func HeldElsewhere(assignees, labels []string, account string) string {
	isOwn := false
	for _, assignee := range assignees {
		if !strings.EqualFold(assignee, account) {
			return "it is assigned to " + assignee
		}
		isOwn = true
	}
	if isOwn {
		return ""
	}
	for _, state := range openStates {
		if slices.Contains(labels, stateLabel(state)) {
			return "another Code Goblins fleet holds it (" + stateLabel(state) + ")"
		}
	}
	return ""
}

// taskSection is the text of a brief's Task section alone, or "" when it has
// none. Headings match whatever their case and a trailing colon.
func taskSection(text string) string {
	var lines []string
	inTask := false
	for _, line := range strings.Split(text, "\n") {
		if heading, ok := strings.CutPrefix(strings.TrimSpace(line), "## "); ok {
			heading = strings.TrimSpace(strings.TrimSuffix(strings.ToLower(strings.TrimSpace(heading)), ":"))
			inTask = heading == "task" || heading == "tasks"
			continue
		}
		if inTask {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}
