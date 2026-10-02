package tickets

import (
	"slices"
	"time"
)

// The windows the report reads: a person is active in a repository when they
// did something there within CollaborationWindow, and a branch is in flight
// when its head is newer than BranchWindow.
const (
	CollaborationWindow = 30 * 24 * time.Hour
	BranchWindow        = 14 * 24 * time.Hour
)

// Contributor is a person other than the Overlord, never a bot, who worked in
// the repository within CollaborationWindow.
type Contributor struct {
	Login        string    `json:"login,omitempty"`
	Name         string    `json:"name,omitempty"`
	AvatarURL    string    `json:"avatar_url,omitempty"`
	LastActive   time.Time `json:"last_active"`
	Commits      int       `json:"commits"`
	Issues       int       `json:"issues_opened"`
	PullRequests int       `json:"pull_requests_opened"`
	Branches     int       `json:"branches_pushed"`
}

// Report is the answer to "what do others have in flight here".
type Report struct {
	Repository string    `json:"repository"`
	Overlord   string    `json:"overlord"`
	ReadAt     time.Time `json:"read_at"`
	// Collaborative is true when anyone besides the Overlord and bots worked
	// here within CollaborationWindow: Contributors is not empty.
	Collaborative bool          `json:"collaborative"`
	Contributors  []Contributor `json:"contributors"`
	Issues        []Issue       `json:"issues"`
	PullRequests  []PullRequest `json:"pull_requests"`
	// Branches are the heads newer than BranchWindow that are not the
	// Overlord's and not the default branch.
	Branches []Branch  `json:"branches"`
	Overlaps *Overlaps `json:"overlaps,omitempty"`
	Unread   []string  `json:"unread,omitempty"`
}

// Overlaps is where in-flight work meets the area a brief or a file list
// names.
type Overlaps struct {
	Area   []string      `json:"area"`
	Files  []FileOverlap `json:"files"`
	Issues []IssueMatch  `json:"issues"`
}

// FileOverlap is one pull request or branch that changes files in the area.
type FileOverlap struct {
	PullRequest int      `json:"pull_request,omitempty"`
	Branch      string   `json:"branch"`
	URL         string   `json:"url,omitempty"`
	Author      Actor    `json:"author"`
	Files       []string `json:"files"`
}

// IssueMatch is one open issue whose text matches the area: it names one of
// its paths, or its title shares enough significant words with the brief.
type IssueMatch struct {
	Number int      `json:"number"`
	Title  string   `json:"title"`
	URL    string   `json:"url"`
	Author Actor    `json:"author"`
	Paths  []string `json:"paths,omitempty"`
	Words  []string `json:"words,omitempty"`
}

// An issue's title matches a brief when the brief uses at least
// MinimumSharedWords of the title's distinct significant words and at least
// MinimumSharedShare of them: a long brief shares three words with almost any
// title, so the share is what tells an issue about the same work from one
// that merely uses the same vocabulary.
const (
	MinimumSharedWords = 3
	MinimumSharedShare = 0.6
)

// Build turns one read into the report. area is nil when no brief or file
// list was given, and the report then names no overlaps. Every list the
// report always carries is a list even when empty, so its JSON never says
// null where a reader expects [].
func Build(activity Activity, now time.Time, area *Area) Report {
	people := contributors(activity, now)
	report := Report{
		Repository:    activity.Repository,
		Overlord:      activity.Viewer.Login,
		ReadAt:        now,
		Collaborative: len(people) > 0,
		Contributors:  people,
		Issues:        append([]Issue{}, activity.Issues...),
		PullRequests:  append([]PullRequest{}, activity.PullRequests...),
		Branches:      listedBranches(activity, now),
		Unread:        slices.Clone(activity.Unread),
	}
	for i := range report.Issues {
		report.Issues[i].Assignees = append([]string{}, report.Issues[i].Assignees...)
		report.Issues[i].Labels = append([]string{}, report.Issues[i].Labels...)
	}
	for i := range report.PullRequests {
		report.PullRequests[i].Files = append([]string{}, report.PullRequests[i].Files...)
	}
	slices.SortFunc(report.Issues, func(a, b Issue) int { return b.Number - a.Number })
	slices.SortFunc(report.PullRequests, func(a, b PullRequest) int { return b.Number - a.Number })
	if area != nil {
		report.Overlaps = overlaps(report, *area)
	}
	return report
}

// BranchesToCompare names the branches the report lists that no open pull
// request heads: the only ones whose changed files must be read separately.
func BranchesToCompare(activity Activity, now time.Time) []string {
	var names []string
	for _, branch := range listedBranches(activity, now) {
		if branch.PullRequest == 0 {
			names = append(names, branch.Name)
		}
	}
	return names
}

// listedBranches are the heads newer than BranchWindow that are neither the
// default branch nor the Overlord's, newest first, each linked to the open
// pull request from this repository that it heads.
func listedBranches(activity Activity, now time.Time) []Branch {
	heads := map[string]int{}
	for _, pull := range activity.PullRequests {
		if !pull.FromFork {
			heads[pull.HeadRef] = pull.Number
		}
	}
	since := now.Add(-BranchWindow)
	branches := []Branch{}
	for _, branch := range activity.Branches {
		if branch.Name == activity.DefaultBranch || branch.CommittedAt.Before(since) || isOverlord(branch.Author, activity.Viewer) {
			continue
		}
		branch.PullRequest = heads[branch.Name]
		branches = append(branches, branch)
	}
	slices.SortStableFunc(branches, func(a, b Branch) int { return b.CommittedAt.Compare(a.CommittedAt) })
	return branches
}

// overlaps names every open pull request and listed branch that changes files
// in the area, and every open issue whose text matches it.
func overlaps(report Report, area Area) *Overlaps {
	result := &Overlaps{Area: append([]string{}, area.Paths...), Files: []FileOverlap{}, Issues: []IssueMatch{}}
	for _, pull := range report.PullRequests {
		if files := filesInArea(area, pull.Files); len(files) > 0 {
			result.Files = append(result.Files, FileOverlap{PullRequest: pull.Number, Branch: pull.HeadRef, URL: pull.URL, Author: pull.Author, Files: files})
		}
	}
	for _, branch := range report.Branches {
		if branch.PullRequest != 0 {
			continue
		}
		if files := filesInArea(area, branch.Files); len(files) > 0 {
			result.Files = append(result.Files, FileOverlap{Branch: branch.Name, Author: branch.Author, Files: files})
		}
	}
	for _, issue := range report.Issues {
		match := IssueMatch{Number: issue.Number, Title: issue.Title, URL: issue.URL, Author: issue.Author}
		mentions := pathMentions(issue.Title + "\n" + issue.Body)
		for _, areaPath := range area.Paths {
			if slices.ContainsFunc(mentions, func(mention string) bool { return coversMention(areaPath, mention) }) {
				match.Paths = append(match.Paths, areaPath)
			}
		}
		if shared, total := sharedWords(area, issue.Title); len(shared) >= MinimumSharedWords && float64(len(shared)) >= MinimumSharedShare*float64(total) {
			match.Words = shared
		}
		if len(match.Paths) > 0 || len(match.Words) > 0 {
			result.Issues = append(result.Issues, match)
		}
	}
	return result
}

// filesInArea returns the files the area covers, sorted.
func filesInArea(area Area, files []string) []string {
	var covered []string
	for _, file := range files {
		if slices.ContainsFunc(area.Paths, func(areaPath string) bool { return coversFile(areaPath, file) }) {
			covered = append(covered, file)
		}
	}
	slices.Sort(covered)
	return covered
}

// sharedWords are the significant words of a title the area's brief also
// uses, as the title writes them, sorted, and how many significant words the
// title has.
func sharedWords(area Area, title string) ([]string, int) {
	var shared []string
	words := significantWords(title)
	for _, w := range words {
		if area.words[w.stem] {
			shared = append(shared, w.written)
		}
	}
	slices.Sort(shared)
	return shared, len(words)
}
