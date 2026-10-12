package tickets

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestShortTitleIsWhatATeammateReads(t *testing.T) {
	long := "DONE 2026-10-06 12:29Z: PR 1493 merged (PrecisionDocs main 756c67205; fresh-worktree gate test step 400 s to 135 s), goblin retired; deploy run 37462939864; Claude Code"
	cases := []struct {
		name  string
		title string
		want  string
	}{
		{name: "a short title stays as it is", title: "Say why a billing sync fails", want: "Say why a billing sync fails"},
		{name: "a trailing harness is dropped", title: "A production deploy never stops on a Fly machine; Claude Code", want: "A production deploy never stops on a Fly machine"},
		{name: "a trailing harness with its effort is dropped", title: "Say why a billing sync fails; Claude Code, max", want: "Say why a billing sync fails"},
		{name: "each harness name is dropped", title: "Say why a billing sync fails; codex", want: "Say why a billing sync fails"},
		{name: "a harness named inside the title stays", title: "Teach the Codex harness to pause", want: "Teach the Codex harness to pause"},
		{name: "a harness word that ends a phrase stays", title: "Compute the area with pi", want: "Compute the area with pi"},
		{name: "spacing and lines are tidied", title: "  Say why a billing\n  sync fails ", want: "Say why a billing sync fails"},
		{name: "a title over 100 characters is cut at a word", title: long, want: "DONE 2026-10-06 12:29Z: PR 1493 merged (PrecisionDocs main 756c67205; fresh-worktree gate test step…"},
		{name: "a title of only a harness is empty", title: "; Claude Code", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			got := ShortTitle(tc.title)

			// Assert
			if got != tc.want {
				t.Fatalf("ShortTitle(%q) = %q, want %q", tc.title, got, tc.want)
			}
			if utf8.RuneCountInString(got) > MaxTitle {
				t.Fatalf("ShortTitle(%q) is %d characters, want at most %d", tc.title, utf8.RuneCountInString(got), MaxTitle)
			}
			if again := ShortTitle(got); again != got {
				t.Fatalf("ShortTitle is not stable: %q became %q", got, again)
			}
		})
	}
}

func TestShortTitleCutsOneLongWordAtTheLimit(t *testing.T) {
	// Act
	got := ShortTitle(strings.Repeat("a", 150))

	// Assert
	if utf8.RuneCountInString(got) != MaxTitle || !strings.HasSuffix(got, "…") {
		t.Fatalf("ShortTitle = %q (%d characters), want %d characters ending in an ellipsis", got, utf8.RuneCountInString(got), MaxTitle)
	}
}

func TestTitleNoteRoundTripsAndIsAbsentUntilWritten(t *testing.T) {
	// Arrange
	dir := t.TempDir()

	// Act
	before, beforeErr := ReadTitleNote(dir, "nw-sync")
	writeErr := WriteTitleNote(dir, "nw-sync", "Say why a billing sync fails")
	after, afterErr := ReadTitleNote(dir, "nw-sync")
	_, invalidErr := ReadTitleNote(dir, "../escape")

	// Assert
	if before != "" || beforeErr != nil {
		t.Fatalf("before writing: %q, %v, want nothing", before, beforeErr)
	}
	if writeErr != nil || afterErr != nil || after != "Say why a billing sync fails" {
		t.Fatalf("after writing: %q, %v, %v", after, writeErr, afterErr)
	}
	if invalidErr == nil || WriteTitleNote(dir, "../escape", "x") == nil {
		t.Fatal("an invalid task id was accepted")
	}
}
