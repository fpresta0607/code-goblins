package tickets

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var (
	// issueRefText is one issue as ParseIssueRef reads it, in lower case.
	issueRefText = regexp.MustCompile(`^(?:https://github\.com/([\w.-]+/[\w.-]+)/issues/(\d+)|([\w.-]+/[\w.-]+)#(\d+))$`)

	qualifiedIssue = regexp.MustCompile(`(?i)([\w.-]+/[\w.-]+)#(\d+)\b`)
	// bareIssue is a #N that stands alone: not the end of owner/name#N and not
	// an HTML entity such as &#39;.
	bareIssue = regexp.MustCompile(`(?:^|[^\w&])#(\d+)\b`)
	// pullRequestWord ends the text before a #N that names a pull request.
	pullRequestWord = regexp.MustCompile(`(?i)\b(?:prs?|pull requests?)\s*$`)

	// issueWordsList is the word issue or issues and what it counts off: one
	// number, or several set apart by commas, "&", "and" or "or", each a
	// number or a range such as "2854 to 2860".
	issueWordsList = regexp.MustCompile(`(?i)\bissues?\s+` + rowIssueItem + `(?:(?:\s*(?:,|&)\s*(?:(?:and|or)\s+)?|\s+(?:and|or)\s+)` + rowIssueItem + `)*`)
	rowIssueNumber = regexp.MustCompile(`(?i)#?(\d+)(?:\s+to\s+#?(\d+))?`)
	// dateTail follows a number that is part of a date or a path, not an issue.
	dateTail = regexp.MustCompile(`^[-/.]\d`)

	// closingKeyword is a word that closes the issue it is followed by, or
	// says a pull request is part of it, and that issue written as #N,
	// owner/name#N or an issue address.
	closingKeyword = regexp.MustCompile(`(?i)\b(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?|part\s+of)\b\s*:?\s*(https://github\.com/[\w.-]+/[\w.-]+/issues/\d+|[\w.-]+/[\w.-]+#\d+|#\d+)`)
)

const (
	// maxIssueRange is the most numbers a row's "issues N to M" is read as.
	maxIssueRange = 50
	// rowIssueItem is one number of a row's issue words, or a range of them.
	rowIssueItem = `#?\d+\b(?:\s+to\s+#?\d+\b)?`
)

// IssueRef is one issue by its repository, owner/name in lower case, and its
// number.
type IssueRef struct {
	Repository string
	Number     int
}

// String is the issue as owner/name#N.
func (r IssueRef) String() string {
	return r.Repository + "#" + strconv.Itoa(r.Number)
}

// URL is the address of the issue on GitHub.
func (r IssueRef) URL() string {
	return "https://github.com/" + r.Repository + "/issues/" + strconv.Itoa(r.Number)
}

// ParseIssueRef reads one issue written as owner/name#N or as its
// https://github.com/owner/name/issues/N address, with surrounding space
// trimmed and its case folded. Anything else, a bare number included, is not
// one.
func ParseIssueRef(text string) (IssueRef, bool) {
	match := issueRefText.FindStringSubmatch(strings.ToLower(strings.TrimSpace(text)))
	if match == nil {
		return IssueRef{}, false
	}
	repository, digits := match[1], match[2]
	if repository == "" {
		repository, digits = match[3], match[4]
	}
	return issueRefOf(repository, digits)
}

// issueRefOf is issue digits of repository, or false when digits are not a
// positive number.
func issueRefOf(repository, digits string) (IssueRef, bool) {
	number, err := strconv.ParseUint(digits, 10, 31)
	if err != nil || number < 1 {
		return IssueRef{}, false
	}
	return IssueRef{Repository: strings.ToLower(repository), Number: int(number)}, true
}

// withIssueRef is refs with ref added, unless it is there already.
func withIssueRef(refs []IssueRef, ref IssueRef) []IssueRef {
	if slices.Contains(refs, ref) {
		return refs
	}
	return append(refs, ref)
}

// BriefIssueRefs are the issues a task's brief names to take up: every issue
// NamedIssues names, as an issue of repository, owner/name, then every issue
// address of any other repository inside the brief's scope, and then those of
// each section the CEO added to the brief later, under a heading that starts
// "## ADDED", since it adds work to the task: the same, read as a Task of its
// own, and those its heading and body name in words, as "issue 42" or
// "issues 34 and 25", the way the CEO writes them there. Each is there once,
// where it is first mentioned.
func BriefIssueRefs(text, repository string) []IssueRef {
	var refs []IssueRef
	for _, named := range NamedIssues(text, repository) {
		if ref, ok := issueRefOf(repository, strconv.Itoa(named.Number)); ok {
			refs = withIssueRef(refs, ref)
		}
	}
	for _, match := range issueURL.FindAllStringSubmatch(briefScope(text), -1) {
		if strings.EqualFold(match[1], repository) {
			continue
		}
		if ref, ok := issueRefOf(match[1], match[2]); ok {
			refs = withIssueRef(refs, ref)
		}
	}
	for _, added := range addedSections(text) {
		for _, ref := range BriefIssueRefs("## Task\n"+added, repository) {
			refs = withIssueRef(refs, ref)
		}
		for _, mention := range issueWordMentions(added, repository) {
			refs = withIssueRef(refs, mention.ref)
		}
	}
	return refs
}

// addedSections are a brief's sections whose heading starts "## ADDED",
// whatever its case, in order, each its heading's words and then its body.
func addedSections(text string) []string {
	var sections []string
	var body []string
	isAdded := false
	for _, line := range strings.Split(text, "\n") {
		if heading, ok := strings.CutPrefix(strings.TrimSpace(line), "## "); ok {
			if isAdded {
				sections = append(sections, strings.Join(body, "\n"))
			}
			isAdded = strings.HasPrefix(strings.ToLower(strings.TrimSpace(heading)), "added")
			body = []string{heading}
			continue
		}
		if isAdded {
			body = append(body, line)
		}
	}
	if isAdded {
		sections = append(sections, strings.Join(body, "\n"))
	}
	return sections
}

// RowIssueRefs are the issues a backlog row's title line names, in the order
// it names them and each once: issue addresses of any repository,
// owner/name#N, a bare #N of repository, and the words "issue N", "issue
// #N", "issues N, M and K" and "issues N to M", whatever their case. A range
// is read only when it spans at most maxIssueRange numbers. A number that no
// issue word or # goes with, as in "PR 2844", "pull request 2844" or a date,
// is not an issue, and neither is a #N right after "PR" or "pull request".
func RowIssueRefs(row, repository string) []IssueRef {
	mentions := issueWordMentions(row, repository)
	add := func(at int, ref IssueRef, ok bool) {
		if ok {
			mentions = append(mentions, issueMention{at, ref})
		}
	}
	for _, match := range issueURL.FindAllStringSubmatchIndex(row, -1) {
		ref, ok := issueRefOf(row[match[2]:match[3]], row[match[4]:match[5]])
		add(match[0], ref, ok)
	}
	for _, match := range qualifiedIssue.FindAllStringSubmatchIndex(row, -1) {
		if match[0] > 0 && row[match[0]-1] == '/' {
			continue
		}
		ref, ok := issueRefOf(row[match[2]:match[3]], row[match[4]:match[5]])
		add(match[0], ref, ok)
	}
	for _, match := range bareIssue.FindAllStringSubmatchIndex(row, -1) {
		if pullRequestWord.MatchString(row[:match[2]-1]) {
			continue
		}
		ref, ok := issueRefOf(repository, row[match[2]:match[3]])
		add(match[2]-1, ref, ok)
	}
	slices.SortStableFunc(mentions, func(a, b issueMention) int { return a.at - b.at })
	var refs []IssueRef
	for _, m := range mentions {
		refs = withIssueRef(refs, m.ref)
	}
	return refs
}

// issueMention is an issue a text names and where it names it.
type issueMention struct {
	at  int
	ref IssueRef
}

// issueWordMentions are the issues of repository that text names in words:
// "issue N", "issue #N", "issues N, M and K" and "issues N to M", whatever
// their case, a range read only when it spans at most maxIssueRange numbers,
// and none whose words run into a date.
func issueWordMentions(text, repository string) []issueMention {
	var mentions []issueMention
	for _, words := range issueWordsList.FindAllStringIndex(text, -1) {
		if dateTail.MatchString(text[words[1]:]) {
			continue
		}
		for _, item := range rowIssueNumber.FindAllStringSubmatchIndex(text[words[0]:words[1]], -1) {
			first, err := strconv.Atoi(text[words[0]+item[2] : words[0]+item[3]])
			if err != nil {
				continue
			}
			last := first
			if item[4] >= 0 {
				if last, err = strconv.Atoi(text[words[0]+item[4] : words[0]+item[5]]); err != nil || last < first || last-first >= maxIssueRange {
					continue
				}
			}
			for number := first; number <= last; number++ {
				if ref, ok := issueRefOf(repository, strconv.Itoa(number)); ok {
					mentions = append(mentions, issueMention{words[0] + item[0], ref})
				}
			}
		}
	}
	return mentions
}

// ClosingIssueRefs are the issues a gate run closes, as no-mistakes stores
// them: one to a line, each "N" of repository or "owner/name#N". Blank and
// unreadable lines are passed over.
func ClosingIssueRefs(value, repository string) []IssueRef {
	var refs []IssueRef
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		ref, ok := ParseIssueRef(line)
		if !ok {
			ref, ok = issueRefOf(repository, line)
		}
		if ok {
			refs = withIssueRef(refs, ref)
		}
	}
	return refs
}

// BodyIssueRefs are the issues a pull request's body names after a closing
// keyword (close, fix or resolve, in any form) or the words "Part of",
// optionally followed by a colon: each written as #N of repository,
// owner/name#N or an issue address. A bare #N anywhere else in the body is not
// named.
func BodyIssueRefs(body, repository string) []IssueRef {
	var refs []IssueRef
	for _, match := range closingKeyword.FindAllStringSubmatch(body, -1) {
		ref, ok := ParseIssueRef(match[1])
		if digits, isBare := strings.CutPrefix(match[1], "#"); isBare {
			ref, ok = issueRefOf(repository, digits)
		}
		if ok {
			refs = withIssueRef(refs, ref)
		}
	}
	return refs
}
