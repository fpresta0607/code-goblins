package supervisor

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/afk"
)

// The 13:18Z report of 2026-10-14 listed eight pull requests that landed by
// train under Not merged, each "No merge was recorded": the CFO had logged
// each with cfo afk log --kind merge --link, which writes no outcome. GitHub's
// word on the pull request is where the outcome comes from.

const repository = "https://github.com/acme/api/pull/"

// handMerge is a merge the CFO logged by hand for pull request number.
func handMerge(number, words string) afk.Entry {
	return afk.Entry{Kind: afk.KindMerge, What: words, Link: repository + number, Evidence: "Train CI tested " + number + " on main 1b94182c9 in one run, 16 of 16 checks pass"}
}

// gitHubSays answers for a pull request by its URL and counts each ask.
type gitHubSays struct {
	mu    sync.Mutex
	state map[string]string
	asked []string
}

func (g *gitHubSays) read(_ context.Context, url string) (PullRequestInfo, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.asked = append(g.asked, url)
	state, ok := g.state[url]
	if !ok {
		return PullRequestInfo{}, errors.New("gh could not read " + url)
	}
	return PullRequestInfo{State: state, Title: "a pull request"}, nil
}

func TestTheReportShowsAHandLoggedMergeOfAMergedPullRequestAsMergedAndAsksGitHubOnce(t *testing.T) {
	// Arrange
	s, h, _ := asTheCFO(t)
	github := &gitHubSays{state: map[string]string{repository + "784": "MERGED", repository + "791": "MERGED", repository + "800": "OPEN"}}
	s.Options.PullRequestState = github.read

	// Act
	awayAndBack(t, s, h,
		handMerge("784", "the button that grants the service"),
		handMerge("784", "the same button again"),
		handMerge("791", "the second landing"),
		handMerge("800", "tried to land it"),
		handMerge("801", "a pull request GitHub cannot read"),
	)

	// Assert
	report, found, err := ReadAFKReport(h)
	if err != nil || !found {
		t.Fatalf("ReadAFKReport = %v, %v, want the report", found, err)
	}
	var headings []string
	for _, section := range report.Sections() {
		headings = append(headings, section.Title+" "+string(rune('0'+len(section.Entries))))
	}
	if got := strings.Join(headings, ","); got != "Not merged 2,Merged 2" {
		t.Errorf("headings = %q, want the two that merged under Merged and the open one and the unread one under Not merged", got)
	}
	slices.Sort(github.asked)
	if want := []string{repository + "784", repository + "791", repository + "800", repository + "801"}; !slices.Equal(github.asked, want) {
		t.Errorf("GitHub was asked about %q, want each pull request once", github.asked)
	}
	if len(report.Notes) != 1 || !strings.Contains(report.Notes[0], repository+"801") {
		t.Errorf("notes = %q, want one that names the pull request GitHub did not answer for", report.Notes)
	}
}

func TestTheReportAsksGitHubNothingWhenEveryMergeSaysHowItEnded(t *testing.T) {
	// Arrange
	s, h, _ := asTheCFO(t)
	github := &gitHubSays{state: map[string]string{}}
	s.Options.PullRequestState = github.read
	landed := handMerge("784", "the button")
	landed.Outcome = afk.OutcomeMerged

	// Act
	awayAndBack(t, s, h, landed)

	// Assert
	if len(github.asked) != 0 {
		t.Errorf("GitHub was asked about %q, want nothing asked of a merge that says how it ended", github.asked)
	}
}
