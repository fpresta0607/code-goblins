package afk

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// The 13:18Z report of 2026-10-14 listed eight pull requests that landed by
// train under Not merged, each "No merge was recorded": the CFO logged each
// by hand with a link and no outcome, and the report files a merge line
// without the outcome merged as not merged. GitHub's own word on the pull
// request is what the outcome comes from.
const (
	handPull   = "https://github.com/acme/api/pull/784"
	handWords  = "button that grants or takes back the service."
	handProved = "Train CI tested 784 on main 1b94182c9 in one run, 16 of 16 checks pass"
)

func handLine(minute int, what, link string) Entry {
	return Entry{At: night.Add(time.Duration(minute) * time.Minute), Kind: KindMerge, What: what, Link: link, Evidence: handProved}
}

func TestAHandLoggedMergeOfAMergedPullRequestReadsAsMerged(t *testing.T) {
	// Arrange
	decisions := Decisions([]Entry{handLine(1, handWords, handPull)})

	// Act
	confirmed := ConfirmMerges(decisions, map[string]bool{handPull: true})
	report := Report{Session: "afk-1", Since: night, Ended: night.Add(time.Hour), From: "the board", EndedFrom: "the board", Decisions: confirmed}
	sections := report.Sections()

	// Assert
	if got := titled(sections); strings.Join(got, ",") != "Merged 1" {
		t.Fatalf("headings = %q, want the pull request under Merged alone:\n%s", got, rendered(t, report))
	}
	merge := sections[0].Entries[0]
	if merge.What != handPull || merge.Outcome != OutcomeMerged || merge.Noted != "button that grants or takes back the service." || merge.Evidence != handProved {
		t.Errorf("the merge = %+v, want the pull request itself with the CFO's words and its evidence", merge)
	}
	if text := rendered(t, report); strings.Contains(text, "No merge was recorded") || strings.Contains(text, "Not merged") {
		t.Errorf("the report still says the merge did not land:\n%s", text)
	}
}

func TestAHandLoggedMergeOfAPullRequestThatDidNotMergeStaysNotMerged(t *testing.T) {
	// Arrange
	decisions := Decisions([]Entry{handLine(1, handWords, handPull)})

	// Act
	confirmed := ConfirmMerges(decisions, map[string]bool{handPull: false})
	report := Report{Session: "afk-1", Since: night, Ended: night.Add(time.Hour), From: "the board", EndedFrom: "the board", Decisions: confirmed}

	// Assert
	if got := titled(report.Sections()); strings.Join(got, ",") != "Not merged 1" {
		t.Errorf("headings = %q, want the pull request that did not merge under Not merged", got)
	}
}

func TestAHandLoggedMergeGitHubWasNotAskedAboutStaysNotMerged(t *testing.T) {
	// Arrange
	decisions := Decisions([]Entry{handLine(1, handWords, handPull)})

	// Act
	confirmed := ConfirmMerges(decisions, map[string]bool{})

	// Assert
	if confirmed[0].Outcome != "" {
		t.Errorf("outcome = %q, want none recorded for a pull request nobody read", confirmed[0].Outcome)
	}
}

func TestOnlyAMergeLineWithNoOutcomeThatNamesAPullRequestIsReadFromGitHub(t *testing.T) {
	// Arrange
	other := "https://github.com/acme/api/pull/791"
	decisions := Decisions([]Entry{
		handLine(1, handWords, handPull),
		handLine(2, "the same pull request again", handPull),
		{At: night.Add(3 * time.Minute), Kind: KindMerge, What: other, Link: other, Evidence: "gh pr merge", Outcome: OutcomeMerged},
		{At: night.Add(4 * time.Minute), Kind: KindMerge, What: "PR landed with no link", Evidence: "read by hand"},
		{At: night.Add(5 * time.Minute), Kind: KindMerge, What: "a deploy run", Link: "https://github.com/acme/api/actions/runs/38270621823", Evidence: "passed"},
		{At: night.Add(6 * time.Minute), Kind: KindDeploy, What: "acme production", Link: handPull, Evidence: "/health reads 200"},
		{At: night.Add(7 * time.Minute), Kind: KindMerge, What: "logged by mistake", Link: "https://github.com/acme/api/pull/792", Evidence: "wrong pull request", Struck: "the wrong pull request"},
	})

	// Act
	asked := UnprovenMerges(decisions)

	// Assert
	if want := []string{handPull}; !slices.Equal(asked, want) {
		t.Errorf("asked about %q, want %q: each pull request once, and no line that already says how it ended", asked, want)
	}
}

// A hand line for a landing the merge train logged too is one merge: the
// train's line carries the CFO's words, and the pull request is told once.
func TestAHandLoggedMergeFoldsIntoTheTrainLineOfTheSamePullRequest(t *testing.T) {
	// Arrange
	train := Entry{At: night, Kind: KindMerge, What: handPull, Link: handPull, Evidence: "merge train acme-1: CI passed on https://github.com/acme/api/pull/900 at abc1234, built on main at 1234567", Outcome: OutcomeMerged}
	decisions := Decisions([]Entry{train, handLine(1, "PR 784 landed on merge train 900", handPull)})

	// Act
	confirmed := ConfirmMerges(decisions, map[string]bool{handPull: true})
	report := Report{Session: "afk-1", Since: night, Ended: night.Add(time.Hour), From: "the board", EndedFrom: "the board", Decisions: confirmed}
	sections := report.Sections()

	// Assert
	if got := titled(sections); strings.Join(got, ",") != "Merged 1" {
		t.Fatalf("headings = %q, want one merge:\n%s", got, rendered(t, report))
	}
	merge := sections[0].Entries[0]
	if merge.What != handPull || merge.Noted != "PR 784 landed on merge train 900. "+handProved+"." {
		t.Errorf("the merge = %+v, want the train's line with the CFO's words on it", merge)
	}
}

func TestAHandLoggedMergeOfASecondPullRequestIsNotFoldedIntoTheFirst(t *testing.T) {
	// Arrange
	second := "https://github.com/acme/api/pull/791"
	decisions := Decisions([]Entry{handLine(1, handWords, handPull), handLine(2, "the second one", second)})

	// Act
	confirmed := ConfirmMerges(decisions, map[string]bool{handPull: true, second: true})
	report := Report{Session: "afk-1", Since: night, Ended: night.Add(time.Hour), From: "the board", EndedFrom: "the board", Decisions: confirmed}

	// Assert
	if got := titled(report.Sections()); strings.Join(got, ",") != "Merged 2" {
		t.Errorf("headings = %q, want both pull requests under Merged", got)
	}
}

func TestPullRequestURLNamesTheLinkOrTheWordsThatAreOne(t *testing.T) {
	for name, tc := range map[string]struct{ link, what, want string }{
		"the link":                  {handPull, "words", handPull},
		"the words when no link":    {"", handPull, handPull},
		"the link before the words": {handPull, "https://github.com/acme/api/pull/9", handPull},
		"a link that is not one":    {"https://github.com/acme/api/actions/runs/1", "words", ""},
		"a files page":              {handPull + "/files", "words", ""},
		"neither":                   {"", "words", ""},
	} {
		t.Run(name, func(t *testing.T) {
			if got := PullRequestURL(tc.link, tc.what); got != tc.want {
				t.Errorf("PullRequestURL(%q, %q) = %q, want %q", tc.link, tc.what, got, tc.want)
			}
		})
	}
}

func TestTwoHandLoggedLinesForOneMergedPullRequestAreOneMergeWithBothSetsOfWords(t *testing.T) {
	// Arrange
	decisions := Decisions([]Entry{handLine(1, handWords, handPull), {At: night.Add(2 * time.Minute), Kind: KindMerge, What: "And again", Link: handPull, Evidence: "Deployed nothing."}})

	// Act
	confirmed := ConfirmMerges(decisions, map[string]bool{handPull: true})
	report := Report{Session: "afk-1", Since: night, Ended: night.Add(time.Hour), From: "the board", EndedFrom: "the board", Decisions: confirmed}
	sections := report.Sections()

	// Assert
	if got := titled(sections); strings.Join(got, ",") != "Merged 1" {
		t.Fatalf("headings = %q, want one merge:\n%s", got, rendered(t, report))
	}
	if got, want := sections[0].Entries[0].Noted, handWords+" And again. Deployed nothing."; got != want {
		t.Errorf("the CFO's words = %q, want %q", got, want)
	}
}
