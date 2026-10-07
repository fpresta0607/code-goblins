package train

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"
)

// ListFields is the --json field list of gh pr list that Riders reads.
const ListFields = "number,url,title,headRefName,headRefOid,baseRefName,isDraft,isCrossRepository,mergeable,reviewDecision,labels,statusCheckRollup,author"

// PullRequest is one open pull request as gh pr list reports it.
type PullRequest struct {
	Number            int     `json:"number"`
	URL               string  `json:"url"`
	Title             string  `json:"title"`
	HeadRefName       string  `json:"headRefName"`
	HeadRefOid        string  `json:"headRefOid"`
	BaseRefName       string  `json:"baseRefName"`
	IsDraft           bool    `json:"isDraft"`
	IsCrossRepository bool    `json:"isCrossRepository"`
	Mergeable         string  `json:"mergeable"`
	ReviewDecision    string  `json:"reviewDecision"`
	Labels            []Label `json:"labels"`
	Checks            []Check `json:"statusCheckRollup"`
	Author            struct {
		Login string `json:"login"`
	} `json:"author"`
}

// Label is one of a pull request's labels.
type Label struct {
	Name string `json:"name"`
}

// Check is one entry of a check rollup: a check run, with a status and a
// conclusion, or a commit status context, with a state.
type Check struct {
	Kind       string `json:"__typename"`
	Name       string `json:"name"`
	Context    string `json:"context"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
	DetailsURL string `json:"detailsUrl"`
	TargetURL  string `json:"targetUrl"`
}

func (c Check) name() string {
	if c.Kind == "StatusContext" {
		return c.Context
	}
	return c.Name
}

func (c Check) isConcluded() bool {
	if c.Kind == "StatusContext" {
		return c.State != "" && c.State != "PENDING" && c.State != "EXPECTED"
	}
	return c.Status == "COMPLETED"
}

// isPassed says whether a concluded check passed: a success, or a check that
// was skipped or neutral, as GitHub's own merge box counts them.
func (c Check) isPassed() bool {
	if c.Kind == "StatusContext" {
		return c.State == "SUCCESS"
	}
	switch c.Conclusion {
	case "SUCCESS", "NEUTRAL", "SKIPPED":
		return true
	}
	return false
}

func (c Check) link() string {
	if c.Kind == "StatusContext" {
		return c.TargetURL
	}
	return c.DetailsURL
}

// checksOutcome says how a rollup stands: "none" with no checks, "pending"
// while any runs, else "passed" or "failed" with the checks that failed.
func checksOutcome(checks []Check) (string, []Check) {
	if len(checks) == 0 {
		return "none", nil
	}
	var failed []Check
	for _, check := range checks {
		if !check.isConcluded() {
			return "pending", nil
		}
		if !check.isPassed() {
			failed = append(failed, check)
		}
	}
	if len(failed) > 0 {
		return "failed", failed
	}
	return "passed", nil
}

// Goblin is a live goblin whose finished pull requests may ride: its task and
// each pull request it reported done since its latest other report, with
// when it reported it.
type Goblin struct {
	Task string
	Done map[string]time.Time
}

// Riders picks the open pull requests that may ride a train onto base, in
// queue order: the one a goblin reported done first goes first. A rider is a
// goblin's finished pull request, opened by viewer, the account gh works as,
// from this repository, green on its own head, not a draft, not held, not in
// conflict with base, wanting no review, and not one a kept train found
// broken at this same head. A teammate's pull request never rides, even one
// a goblin reported. left says, for each other pull request into base, why
// it does not ride.
func Riders(open []PullRequest, base, viewer string, goblins []Goblin, past []Train) (riders []Car, left []string) {
	type queued struct {
		car  Car
		done time.Time
	}
	var waiting []queued
	for _, pr := range open {
		if pr.BaseRefName != base || strings.HasPrefix(pr.HeadRefName, BranchPrefix) {
			continue
		}
		task, done := ownerOf(goblins, pr.URL)
		why := ""
		switch outcome, _ := checksOutcome(pr.Checks); {
		case task == "":
			why = "no goblin reported it done"
		case pr.Author.Login != viewer:
			why = "it was opened by " + pr.Author.Login + ", not by " + viewer + ", the account the fleet works as"
		case pr.IsDraft:
			why = "it is a draft"
		case isHeld(pr):
			why = "it is held: it carries the " + HoldLabel + " label"
		case pr.IsCrossRepository:
			why = "it comes from another repository"
		case pr.Mergeable == "CONFLICTING":
			why = "it conflicts with " + base
		case pr.ReviewDecision == "CHANGES_REQUESTED" || pr.ReviewDecision == "REVIEW_REQUIRED":
			why = "it waits on a review"
		case outcome == "none":
			why = "it has no checks"
		case outcome == "pending":
			why = "its checks are still running"
		case outcome == "failed":
			why = "its checks failed"
		default:
			if broke, found := brokeAt(past, pr.URL, pr.HeadRefOid); found {
				why = fmt.Sprintf("it broke train %s at this head, and rides again once its head changes", broke)
			}
		}
		if why != "" {
			left = append(left, fmt.Sprintf("#%d %s", pr.Number, why))
			continue
		}
		waiting = append(waiting, queued{Car{Number: pr.Number, URL: pr.URL, Title: pr.Title, Branch: pr.HeadRefName, Head: pr.HeadRefOid, Task: task, State: CarRiding}, done})
	}
	slices.SortStableFunc(waiting, func(a, b queued) int {
		return cmp.Or(a.done.Compare(b.done), cmp.Compare(a.car.Number, b.car.Number))
	})
	for _, next := range waiting {
		riders = append(riders, next.car)
	}
	return riders, left
}

// ownerOf names the goblin that reported url done, and when.
func ownerOf(goblins []Goblin, url string) (string, time.Time) {
	for _, goblin := range goblins {
		if done, ok := goblin.Done[url]; ok {
			return goblin.Task, done
		}
	}
	return "", time.Time{}
}

func isHeld(pr PullRequest) bool {
	return slices.ContainsFunc(pr.Labels, func(label Label) bool { return strings.EqualFold(label.Name, HoldLabel) })
}

// brokeAt names the kept train that found url broken at head, if one did.
func brokeAt(past []Train, url, head string) (string, bool) {
	for _, t := range past {
		for _, car := range t.Cars {
			if car.State == CarCulprit && car.URL == url && car.Head == head {
				return t.ID, true
			}
		}
	}
	return "", false
}
