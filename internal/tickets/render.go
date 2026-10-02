package tickets

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// filesShown is how many of a pull request's files the text report prints;
// --json lists every one.
const filesShown = 8

// RenderJSON writes the report as indented JSON.
func RenderJSON(w io.Writer, report Report) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

// RenderText writes the report for the CFO to read before a dispatch.
func RenderText(w io.Writer, report Report) error {
	var b strings.Builder
	fmt.Fprintf(&b, "%s, read %s\n", report.Repository, report.ReadAt.UTC().Format("2006-01-02 15:04Z"))
	if report.Collaborative {
		fmt.Fprintf(&b, "Collaborative: %s besides %s worked here in the last 30 days.\n", countOf(len(report.Contributors), "person", "people"), report.Overlord)
	} else {
		fmt.Fprintf(&b, "Not collaborative: nobody besides %s and bots worked here in the last 30 days.\n", report.Overlord)
	}
	for _, unread := range report.Unread {
		fmt.Fprintf(&b, "Not read: %s.\n", unread)
	}

	if len(report.Contributors) > 0 {
		fmt.Fprintf(&b, "\nContributors\n")
		for _, person := range report.Contributors {
			var did []string
			for _, tally := range []struct {
				count            int
				singular, plural string
			}{
				{person.PullRequests, "PR opened", "PRs opened"},
				{person.Issues, "issue opened", "issues opened"},
				{person.Commits, "commit", "commits"},
				{person.Branches, "branch pushed", "branches pushed"},
			} {
				if tally.count > 0 {
					did = append(did, countOf(tally.count, tally.singular, tally.plural))
				}
			}
			fmt.Fprintf(&b, "- %s, active %s ago: %s\n", actorName(Actor{Login: person.Login, Name: person.Name}), age(report.ReadAt, person.LastActive), strings.Join(did, ", "))
		}
	}

	fmt.Fprintf(&b, "\nOpen pull requests (%d)\n", len(report.PullRequests))
	for _, pull := range report.PullRequests {
		draft := ""
		if pull.IsDraft {
			draft = "[draft] "
		}
		fmt.Fprintf(&b, "- #%d %s%s (%s, opened %s ago, %s, branch %s) %s\n", pull.Number, draft, pull.Title, actorName(pull.Author), age(report.ReadAt, pull.CreatedAt), countOf(len(pull.Files), "file", "files"), pull.HeadRef, pull.URL)
		for i, file := range pull.Files {
			if i == filesShown {
				fmt.Fprintf(&b, "    and %d more (--json lists every file)\n", len(pull.Files)-filesShown)
				break
			}
			fmt.Fprintf(&b, "    %s\n", file)
		}
	}

	fmt.Fprintf(&b, "\nOpen issues (%d)\n", len(report.Issues))
	for _, issue := range report.Issues {
		fmt.Fprintf(&b, "- #%d %s (%s, opened %s ago; assigned %s; labels %s) %s\n", issue.Number, issue.Title, actorName(issue.Author), age(report.ReadAt, issue.CreatedAt), listOrNone(issue.Assignees), listOrNone(issue.Labels), issue.URL)
	}

	fmt.Fprintf(&b, "\nBranches others pushed in the last 14 days (%d)\n", len(report.Branches))
	for _, branch := range report.Branches {
		pull := "no PR"
		if branch.PullRequest != 0 {
			pull = fmt.Sprintf("PR #%d", branch.PullRequest)
		}
		fmt.Fprintf(&b, "- %s (%s, last commit %s ago, %s)\n", branch.Name, actorName(branch.Author), age(report.ReadAt, branch.CommittedAt), pull)
	}

	if overlaps := report.Overlaps; overlaps != nil {
		fmt.Fprintf(&b, "\nOverlaps with %s\n", listOrNone(overlaps.Area))
		if len(overlaps.Files) == 0 && len(overlaps.Issues) == 0 {
			fmt.Fprintf(&b, "- none\n")
		}
		for _, line := range overlaps.Lines() {
			fmt.Fprintf(&b, "- %s\n", line)
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func actorName(actor Actor) string {
	switch {
	case actor.Login != "":
		return actor.Login
	case actor.Name != "":
		return actor.Name + " (no GitHub account)"
	default:
		return "a deleted account"
	}
}

func countOf(count int, singular, plural string) string {
	if count == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %s", count, plural)
}

func listOrNone(items []string) string {
	if len(items) == 0 {
		return "none"
	}
	return strings.Join(items, ", ")
}

// age says how long ago a time was, in the largest whole unit that fits.
func age(now, then time.Time) string {
	elapsed := now.Sub(then)
	switch {
	case elapsed < time.Hour:
		return fmt.Sprintf("%dm", int(elapsed.Minutes()))
	case elapsed < 48*time.Hour:
		return fmt.Sprintf("%dh", int(elapsed.Hours()))
	default:
		return fmt.Sprintf("%dd", int(elapsed.Hours()/24))
	}
}
