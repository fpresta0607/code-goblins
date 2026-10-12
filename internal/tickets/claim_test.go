package tickets

import (
	"slices"
	"testing"
)

func TestNamedIssuesReadsEveryIssueTheTaskTakesUp(t *testing.T) {
	cases := []struct {
		name string
		text string
		want []NamedIssue
	}{
		{name: "an issue URL in the repository", text: "## Task\n\nFix https://github.com/fpresta0607/northwind-api/issues/415 for good.\n", want: []NamedIssue{{Number: 415}}},
		{name: "the URL's owner and name in another case", text: "## Task\n\nSee https://github.com/FPresta0607/Northwind-API/issues/415.\n", want: []NamedIssue{{Number: 415}}},
		{name: "the words issue #N", text: "## Task\n\nResolve issue #415, which a teammate filed.\n", want: []NamedIssue{{Number: 415}}},
		{name: "the words issues #N", text: "## Task\n\nResolve issues #415.\n", want: []NamedIssue{{Number: 415}}},
		{name: "one issue beside a bare pull request number", text: "## Task\n\nResolve issue #415 and PR #412.\n", want: []NamedIssue{{Number: 415}}},
		{name: "the same issue named twice", text: "## Task\n\nResolve Issue #415.\n\n## Acceptance criteria\n\n- https://github.com/fpresta0607/northwind-api/issues/415 is closed.\n", want: []NamedIssue{{Number: 415}}},
		{name: "two different issues, in the order the brief names them", text: "## Task\n\nResolve issue #416 and issue #415.\n", want: []NamedIssue{{Number: 416}, {Number: 415}}},
		{name: "issues followed by a list joined by and", text: "## Task\n\nResolve issues #415 and #416.\n", want: []NamedIssue{{Number: 415}, {Number: 416}}},
		{name: "issue followed by a list joined by and", text: "## Task\n\nResolve issue #415 and #416.\n", want: []NamedIssue{{Number: 415}, {Number: 416}}},
		{name: "issues followed by a list joined by commas, or and &", text: "## Task\n\nResolve issues #415, #416 or #417 & #418.\n", want: []NamedIssue{{Number: 415}, {Number: 416}, {Number: 417}, {Number: 418}}},
		{name: "two bare #N lines of the Task", text: "## Task\n\nClose two open issues:\n\n- #1343 Project Home reads an unfinished search as a miss.\n- #1344 The ordinance badge still reads pending.\n", want: []NamedIssue{{Number: 1343, IsListed: true}, {Number: 1344, IsListed: true}}},
		{name: "bare #N lines as a numbered list, a heading and a plain line", text: "## Task\n\n1. #1343 first\n### #1344 second\n#1263 and #1260 are one bug.\n", want: []NamedIssue{{Number: 1343, IsListed: true}, {Number: 1344, IsListed: true}, {Number: 1263, IsListed: true}, {Number: 1260, IsListed: true}}},
		{name: "a bare #N line that the words name too", text: "## Task\n\n- #415 the sync fails\n\nResolve issue #415.\n", want: []NamedIssue{{Number: 415}}},
		{name: "a bare number inside a sentence, which briefs use for pull requests", text: "## Task\n\nPR #412 changes the same files, see #413.\n"},
		{name: "a bare #N line outside the Task", text: "## Task\n\nSay why the sync fails.\n\n## Acceptance criteria\n\n- #415: a red test first.\n"},
		{name: "a pull request URL", text: "## Task\n\nSee https://github.com/fpresta0607/northwind-api/pull/412.\n"},
		{name: "an issue in another repository", text: "## Task\n\nLike https://github.com/fpresta0607/northwind-web/issues/415.\n"},
		{name: "an issue named only in Constraints", text: "## Task\n\nSay why the sync fails.\n\n## Constraints\n\n- issue #415 belongs to nw-other.\n- #416 is Nathan's.\n"},
		{name: "a backlog row's text with no Task", text: "- #415 the sync fails\n"},
		{name: "no issue at all", text: "## Task\n\nSay why the sync fails.\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			got := NamedIssues(tc.text, "fpresta0607/northwind-api")

			// Assert
			if !slices.Equal(got, tc.want) {
				t.Fatalf("NamedIssues = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestHeldElsewhereSaysWhenSomeoneElseHasAnIssue(t *testing.T) {
	cases := []struct {
		name      string
		assignees []string
		labels    []string
		want      string
	}{
		{name: "assigned to someone else", assignees: []string{"nreid1609"}, want: "it is assigned to nreid1609"},
		{name: "assigned to the account and someone else", assignees: []string{"fpresta0607", "nreid1609"}, want: "it is assigned to nreid1609"},
		{name: "another fleet's state label with nobody assigned", labels: []string{"bug", "cfo: pr open"}, want: "another Code Goblins fleet holds it (cfo: pr open)"},
		{name: "assigned to the account, its name in another case", assignees: []string{"FPresta0607"}, labels: []string{"cfo: in progress"}},
		{name: "a harness label alone", labels: []string{"goblin: claude"}},
		{name: "free"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			got := HeldElsewhere(tc.assignees, tc.labels, "fpresta0607")

			// Assert
			if got != tc.want {
				t.Fatalf("HeldElsewhere = %q, want %q", got, tc.want)
			}
		})
	}
}
