package tickets

import (
	"fmt"
	"slices"
	"testing"
)

func issueRefs(repository string, numbers ...int) []IssueRef {
	var refs []IssueRef
	for _, number := range numbers {
		refs = append(refs, IssueRef{Repository: repository, Number: number})
	}
	return refs
}

func TestIssueRefWritesItselfAsAnIdentifierAndAnAddress(t *testing.T) {
	// Arrange
	ref := IssueRef{Repository: "acme/widgets", Number: 7}

	// Act
	identifier, address := ref.String(), ref.URL()

	// Assert
	if identifier != "acme/widgets#7" || address != "https://github.com/acme/widgets/issues/7" {
		t.Fatalf("String = %q, URL = %q", identifier, address)
	}
}

func TestParseIssueRefReadsOnlyAFullyNamedIssue(t *testing.T) {
	cases := []struct {
		name, text string
		want       IssueRef
		isIssue    bool
	}{
		{name: "owner name and number", text: "acme/widgets#7", want: IssueRef{"acme/widgets", 7}, isIssue: true},
		{name: "address", text: "https://github.com/acme/widgets/issues/7", want: IssueRef{"acme/widgets", 7}, isIssue: true},
		{name: "space is trimmed and case folded", text: "  Acme/Widgets#7\n", want: IssueRef{"acme/widgets", 7}, isIssue: true},
		{name: "address case is folded", text: "https://github.com/Acme/Widgets/issues/7", want: IssueRef{"acme/widgets", 7}, isIssue: true},
		{name: "bare number", text: "7"},
		{name: "bare hash number", text: "#7"},
		{name: "pull request address", text: "https://github.com/acme/widgets/pull/7"},
		{name: "address with a tail", text: "https://github.com/acme/widgets/issues/7/extra"},
		{name: "zero", text: "acme/widgets#0"},
		{name: "number too large", text: "acme/widgets#99999999999"},
		{name: "empty", text: ""},
		{name: "words around it", text: "see acme/widgets#7"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			got, isIssue := ParseIssueRef(tc.text)

			// Assert
			if isIssue != tc.isIssue || got != tc.want {
				t.Fatalf("ParseIssueRef(%q) = %v, %v, want %v, %v", tc.text, got, isIssue, tc.want, tc.isIssue)
			}
		})
	}
}

func TestBriefIssueRefsNamesTheRepositorysIssuesThenOtherRepositoriesAddresses(t *testing.T) {
	cases := []struct {
		name, text string
		want       []IssueRef
	}{
		{
			name: "own issues first then foreign addresses, each once",
			text: "## Task\nFix https://github.com/Other/Repo/issues/5 and issue #3, also https://github.com/acme/widgets/issues/9.\nSee https://github.com/other/repo/issues/5 again.\n",
			want: append(issueRefs("acme/widgets", 3, 9), IssueRef{"other/repo", 5}),
		},
		{
			name: "a listed bare number is named",
			text: "## Task\n- #1343 Project Home\n",
			want: issueRefs("acme/widgets", 1343),
		},
		{
			name: "constraints are not read",
			text: "## Task\nFix issue #3.\n## Constraints\nDo not touch https://github.com/other/repo/issues/5 or issue #4.\n",
			want: issueRefs("acme/widgets", 3),
		},
		{
			name: "a section the CEO added later is read as the task's own",
			text: "## Task\nFix issue #3.\n## Constraints\nLeave issue #4 alone.\n## ADDED 2026-11-19 about 22:40Z - the stats page, issue 42, and issues 34 and 25\nIssue: https://github.com/acme/widgets/issues/188.\n- #190 the toggle\nAlso https://github.com/other/repo/issues/7.\n1. Issue 50, the tab left open.\nThe other task (#185) adds a picker, so leave it.\n",
			want: append(append(issueRefs("acme/widgets", 3, 188, 190), IssueRef{"other/repo", 7}), issueRefs("acme/widgets", 42, 34, 25, 50)...),
		},
		{
			name: "a heading that starts Added in another case is not an added section",
			text: "## Task\nTidy the docs.\n## Added context\nSee issue 12 for background.\n",
		},
		{name: "no issue", text: "## Task\nTidy the docs.\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			got := BriefIssueRefs(tc.text, "acme/widgets")

			// Assert
			if !slices.Equal(got, tc.want) {
				t.Fatalf("BriefIssueRefs = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRowIssueRefsReadsTheIssuesATitleLineNames(t *testing.T) {
	cases := []struct {
		name, row string
		want      []IssueRef
	}{
		{name: "issue with a repository note", row: "find the root cause and fix it, issue 192 (repo: SIQshift)", want: issueRefs("acme/widgets", 192)},
		{name: "list with a comma and", row: "issues 2901, 2900 and 2580", want: issueRefs("acme/widgets", 2901, 2900, 2580)},
		{name: "list with an oxford and", row: "issues 1, 2, and 3", want: issueRefs("acme/widgets", 1, 2, 3)},
		{name: "list with ampersand and or", row: "Issues #1 & #2 or 3", want: issueRefs("acme/widgets", 1, 2, 3)},
		{name: "range", row: "issues 2854 to 2860", want: issueRefs("acme/widgets", 2854, 2855, 2856, 2857, 2858, 2859, 2860)},
		{name: "range of exactly fifty", row: "issues 1 to 50", want: issueRefs("acme/widgets", func() []int {
			numbers := make([]int, 50)
			for index := range numbers {
				numbers[index] = index + 1
			}
			return numbers
		}()...)},
		{name: "range wider than fifty is ignored", row: "issues 1 to 51", want: nil},
		{name: "backwards range is ignored", row: "issues 9 to 3", want: nil},
		{name: "owner name and number", row: "Issue fpresta0607/peakCraftsman#68", want: []IssueRef{{"fpresta0607/peakcraftsman", 68}}},
		{name: "owner name and number after issue", row: "issue fpresta0607/russo-pool-spa#1", want: []IssueRef{{"fpresta0607/russo-pool-spa", 1}}},
		{name: "address of another repository", row: "fix https://github.com/Other/Repo/issues/12 now", want: []IssueRef{{"other/repo", 12}}},
		{name: "bare hash number", row: "fix #44 in the board", want: issueRefs("acme/widgets", 44)},
		{name: "in the order named, once each", row: "#5, issue 4, other/repo#6, issue #5, issue 4", want: append(issueRefs("acme/widgets", 5, 4), IssueRef{"other/repo", 6})},
		{name: "pull request is not an issue", row: "land PR 2844 and pull request 2845", want: nil},
		{name: "hash after PR is not an issue", row: "land PR #2844 and pull request #2845", want: nil},
		{name: "date is not an issue", row: "release notes for 2026-11-19", want: nil},
		{name: "date after the word issue is not an issue", row: "issue 2026-11-19", want: nil},
		{name: "entity is not an issue", row: "quote &#39;it&#39;", want: nil},
		{name: "path with a hash is not a repository", row: "see docs/guide/readme#3", want: nil},
		{name: "word inside another word", row: "reissue 5", want: nil},
		{name: "zero is not an issue", row: "issue 0", want: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			got := RowIssueRefs(tc.row, "Acme/Widgets")

			// Assert
			if !slices.Equal(got, tc.want) {
				t.Fatalf("RowIssueRefs(%q) = %v, want %v", tc.row, got, tc.want)
			}
		})
	}
}

func TestClosingIssueRefsReadsOneIssueToALine(t *testing.T) {
	cases := []struct {
		name, value string
		want        []IssueRef
	}{
		{name: "numbers and qualified issues", value: "12\nOther/Repo#3\n 14 \n", want: append(issueRefs("acme/widgets", 12), IssueRef{"other/repo", 3}, IssueRef{"acme/widgets", 14})},
		{name: "windows line ends", value: "12\r\n13\r\n", want: issueRefs("acme/widgets", 12, 13)},
		{name: "blank and unreadable lines are passed over", value: "\nnot an issue\n+5\n-5\n0\n12\n", want: issueRefs("acme/widgets", 12)},
		{name: "each issue once", value: "12\nacme/widgets#12\n", want: issueRefs("acme/widgets", 12)},
		{name: "empty", value: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			got := ClosingIssueRefs(tc.value, "Acme/Widgets")

			// Assert
			if !slices.Equal(got, tc.want) {
				t.Fatalf("ClosingIssueRefs(%q) = %v, want %v", tc.value, got, tc.want)
			}
		})
	}
}

func TestBodyIssueRefsReadsOnlyWhatAKeywordNames(t *testing.T) {
	cases := []struct {
		body string
		want []IssueRef
	}{
		{body: "Closes #5", want: issueRefs("acme/widgets", 5)},
		{body: "fixed: #6\nResolves other/repo#7\nPart of https://github.com/Third/Repo/issues/8", want: []IssueRef{{"acme/widgets", 6}, {"other/repo", 7}, {"third/repo", 8}}},
		{body: "CLOSE #1, close #2, Fix #3, FIXES #4, resolve #5, Resolved #6, closed #7, fixed #8, resolves #9", want: issueRefs("acme/widgets", 1, 2, 3, 4, 5, 6, 7, 8, 9)},
		{body: "PART OF: #10", want: issueRefs("acme/widgets", 10)},
		{body: "Closes #5 and Fixes #5", want: issueRefs("acme/widgets", 5)},
		{body: "See #5 and the discussion in #6", want: nil},
		{body: "prefix #5 and disclose #6", want: nil},
		{body: "Closes the loop\n#5", want: nil},
		{body: "Part of the work, issue 3", want: nil},
		{body: "", want: nil},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%q", tc.body), func(t *testing.T) {
			// Act
			got := BodyIssueRefs(tc.body, "Acme/Widgets")

			// Assert
			if !slices.Equal(got, tc.want) {
				t.Fatalf("BodyIssueRefs(%q) = %v, want %v", tc.body, got, tc.want)
			}
		})
	}
}
