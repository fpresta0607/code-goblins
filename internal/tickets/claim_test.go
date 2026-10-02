package tickets

import "testing"

func TestClaimedIssueReadsOneIssueTheTaskNames(t *testing.T) {
	cases := []struct {
		name   string
		text   string
		want   int
		wantOK bool
	}{
		{name: "an issue URL in the repository", text: "## Task\n\nFix https://github.com/fpresta0607/northwind-api/issues/415 for good.\n", want: 415, wantOK: true},
		{name: "the URL's owner and name in another case", text: "## Task\n\nSee https://github.com/FPresta0607/Northwind-API/issues/415.\n", want: 415, wantOK: true},
		{name: "the words issue #N", text: "## Task\n\nResolve issue #415, which a teammate filed.\n", want: 415, wantOK: true},
		{name: "the words issues #N", text: "## Task\n\nResolve issues #415.\n", want: 415, wantOK: true},
		{name: "one issue beside a bare pull request number", text: "## Task\n\nResolve issue #415 and PR #412.\n", want: 415, wantOK: true},
		{name: "the same issue named twice", text: "## Task\n\nResolve Issue #415.\n\n## Acceptance criteria\n\n- https://github.com/fpresta0607/northwind-api/issues/415 is closed.\n", want: 415, wantOK: true},
		{name: "a bare number, which briefs use for pull requests", text: "## Task\n\nPR #412 changes the same files.\n"},
		{name: "a pull request URL", text: "## Task\n\nSee https://github.com/fpresta0607/northwind-api/pull/412.\n"},
		{name: "an issue in another repository", text: "## Task\n\nLike https://github.com/fpresta0607/northwind-web/issues/415.\n"},
		{name: "two different issues", text: "## Task\n\nResolve issue #415 and issue #416.\n"},
		{name: "issues followed by a list joined by and", text: "## Task\n\nResolve issues #415 and #416.\n"},
		{name: "issue followed by a list joined by and", text: "## Task\n\nResolve issue #415 and #416.\n"},
		{name: "issues followed by a list joined by commas, or and &", text: "## Task\n\nResolve issues #415, #416 or #417 & #418.\n"},
		{name: "an issue named only in Constraints", text: "## Task\n\nSay why the sync fails.\n\n## Constraints\n\n- issue #415 belongs to nw-other.\n"},
		{name: "no issue at all", text: "## Task\n\nSay why the sync fails.\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			got, ok := ClaimedIssue(tc.text, "fpresta0607/northwind-api")

			// Assert
			if got != tc.want || ok != tc.wantOK {
				t.Fatalf("ClaimedIssue = %d, %v, want %d, %v", got, ok, tc.want, tc.wantOK)
			}
		})
	}
}
