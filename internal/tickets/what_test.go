package tickets

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTicketSectionIsTheOneBriefTextATicketCarries(t *testing.T) {
	cases := []struct {
		name  string
		brief string
		want  string
	}{
		{name: "a section of sentences on their own lines", brief: "## Task\n\nRewrite internal/billing/sync.go.\n\n## Ticket\n\nThe billing sync says why it failed.\nIt names the invoice it stopped on.\n\n## Constraints\n\nKeep off data/nw-sync/notes.md.\n",
			want: "The billing sync says why it failed. It names the invoice it stopped on."},
		{name: "a heading in another case with a colon", brief: "## ticket:\n\nThe billing sync says why it failed.\n", want: "The billing sync says why it failed."},
		{name: "a brief with no section", brief: "## Task\n\nThe billing sync says why it failed.\n"},
		{name: "an empty section", brief: "## Ticket\n\n## Task\n\nThe billing sync says why it failed.\n"},
		{name: "a line that starts with an issue number stays in it", brief: "## Ticket\n\n#1448 asks for this.\nIt adds the layer.\n", want: "#1448 asks for this. It adds the layer."},
		{name: "a deeper heading inside the section ends it", brief: "## Ticket\n\nThe billing sync says why it failed.\n\n### Notes for the CFO\n\nAsk the Overlord first.\n", want: "The billing sync says why it failed."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			got := TicketSection(tc.brief)

			// Assert
			if got != tc.want {
				t.Fatalf("TicketSection = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTicketSectionIsCutWhenItRunsLong(t *testing.T) {
	// Act
	got := TicketSection("## Ticket\n\n" + strings.Repeat("The billing sync says why. ", 60))

	// Assert
	if utf8.RuneCountInString(got) > maxWhat || !strings.HasSuffix(got, "…") {
		t.Fatalf("TicketSection is %d characters, want at most %d ending in an ellipsis", utf8.RuneCountInString(got), maxWhat)
	}
}
