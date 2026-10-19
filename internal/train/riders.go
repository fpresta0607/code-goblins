package train

import (
	"cmp"
	"encoding/json"
	"fmt"
	"net/url"
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
	Checks            Checks  `json:"statusCheckRollup"`
	Author            struct {
		Login string `json:"login"`
	} `json:"author"`
}

// DraftStateCheck is the check of the workflows' draft job, which runs only
// when a pull request event saw a draft and reads whether the pull request is
// a draft now, so a push an event saw as a draft still tests the head of one
// marked ready meanwhile (#963). It tests nothing: on a draft it succeeds
// while every other job is skipped, so counted it made a head that only a
// draft's run had seen look tested.
const DraftStateCheck = "draft state"

// Checks is a head's check rollup as gh reports it, less the draft job's
// check, which tests nothing (DraftStateCheck), whatever it concluded.
type Checks []Check

func (c *Checks) UnmarshalJSON(data []byte) error {
	var rollup []Check
	if err := json.Unmarshal(data, &rollup); err != nil {
		return err
	}
	*c = slices.DeleteFunc(rollup, func(check Check) bool { return check.Kind == "CheckRun" && check.Name == DraftStateCheck })
	return nil
}

// Label is one of a pull request's labels.
type Label struct {
	Name string `json:"name"`
}

// IsRateLimitedDeploy says whether a commit status, by its state and target
// page, is a deploy the host refused for its rate limit: Vercel, once a day's
// builds are spent, fails the status and points it at its upgrade page for
// that limit, upgradeToPro=build-rate-limit (#1194). gh's check rollup gives
// a status's target but never its description, so the target is what says
// so. The host never built the head, so the status says nothing of the
// change: it counts as not run, like a skipped check, and pushing again for a
// fresh one spends another build of the same allowance. Any other failed
// status still fails.
func IsRateLimitedDeploy(state, targetURL string) bool {
	target, err := url.Parse(targetURL)
	return state == "FAILURE" && err == nil && target.Host == "vercel.com" && target.Query().Get("upgradeToPro") == "build-rate-limit"
}

// Check is one entry of a check rollup: a check run, with a status and a
// conclusion, or a commit status context, with a state and a target page.
type Check struct {
	Kind       string `json:"__typename"`
	Name       string `json:"name"`
	Context    string `json:"context"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
	DetailsURL string `json:"detailsUrl"`
	TargetURL  string `json:"targetUrl"`
	// StartedAt and CompletedAt are when GitHub says a check run started and
	// finished. A commit status has only the first, the time it was set.
	StartedAt   string `json:"startedAt"`
	CompletedAt string `json:"completedAt"`
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

// isRateLimited says whether the check is a deploy the host rate limited.
func (c Check) isRateLimited() bool {
	return c.Kind == "StatusContext" && IsRateLimitedDeploy(c.State, c.TargetURL)
}

// isPassed says whether a concluded check passed: a success, or a check that
// was skipped or neutral, as GitHub's own merge box counts them, or a deploy
// the host rate limited, which never ran.
func (c Check) isPassed() bool {
	if c.Kind == "StatusContext" {
		return c.State == "SUCCESS" || c.isRateLimited()
	}
	switch c.Conclusion {
	case "SUCCESS", "NEUTRAL", "SKIPPED":
		return true
	}
	return false
}

// finished is when the check finished, or zero when GitHub did not say.
func (c Check) finished() time.Time {
	said := c.CompletedAt
	if c.Kind == "StatusContext" {
		said = c.StartedAt
	}
	at, _ := time.Parse(time.RFC3339, said)
	return at
}

func (c Check) link() string {
	if c.Kind == "StatusContext" {
		return c.TargetURL
	}
	return c.DetailsURL
}

// hasRun says whether a concluded check ran: one that was skipped or
// neutral tested nothing, though GitHub's merge box counts it as passing,
// and a deploy the host rate limited never built the head.
func (c Check) hasRun() bool {
	if c.Kind == "StatusContext" {
		return !c.isRateLimited()
	}
	return c.Conclusion != "SKIPPED" && c.Conclusion != "NEUTRAL"
}

// rateLimited names the deploys in checks that the host rate limited.
func rateLimited(checks []Check) string {
	var names []string
	for _, check := range checks {
		if check.isRateLimited() {
			names = append(names, check.name())
		}
	}
	return strings.Join(names, ", ")
}

// checksOutcome says how a rollup stands: "none" with no checks or none that
// ran, "pending" while any runs, else "passed" or "failed" with the checks
// that failed. A draft's run skips its jobs, so a pull request marked ready
// shows its draft run's skipped checks until its ready run registers, and
// those are no evidence that anything passed. A deploy the host rate limited
// counts as not run in the same way, never as failed.
func checksOutcome(checks []Check) (string, []Check) {
	var failed []Check
	hasRun := false
	for _, check := range checks {
		if !check.isConcluded() {
			return "pending", nil
		}
		hasRun = hasRun || check.hasRun()
		if !check.isPassed() {
			failed = append(failed, check)
		}
	}
	switch {
	case len(failed) > 0:
		return "failed", failed
	case !hasRun:
		return "none", nil
	}
	return "passed", nil
}

// Goblin is a live goblin whose finished pull requests may ride: its task and
// each pull request it reported done since the task was spawned, whatever it
// reported after, with when it first reported it.
type Goblin struct {
	Task string
	// Name and Title are the goblin's fun name and title.
	Name  string
	Title string
	Done  map[string]time.Time
	// Reported is when it last reported each pull request done, and
	// Relaunched when its terminal last started: at its spawn, or since, at a
	// pause's resume, a switch or a comeback after a restart.
	Reported   map[string]time.Time
	Relaunched time.Time
}

// isOutdated says owner's report of pr done no longer stands for the head pr
// has now. A report made since the goblin's terminal last started stands
// whatever came after, as it always did. One made before that, as before a
// pause, stands only while every check of the head had finished by the time
// of the report: the pull request was green then and is unchanged since. A
// check that finished after the report, or whose time GitHub does not give,
// means a new head or a new run, which the goblin has not reported done.
func isOutdated(owner Goblin, pr PullRequest) bool {
	reported, isReported := owner.Reported[pr.URL]
	if !isReported || !reported.Before(owner.Relaunched.Truncate(time.Second)) {
		return false
	}
	return slices.ContainsFunc(pr.Checks, func(check Check) bool {
		finished := check.finished()
		return finished.IsZero() || finished.After(reported)
	})
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
		owner, done := ownerOf(goblins, pr.URL)
		limited := rateLimited(pr.Checks)
		why := ""
		switch outcome, _ := checksOutcome(pr.Checks); {
		case owner.Task == "":
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
		case outcome == "none" && len(pr.Checks) == 0:
			why = "it has no checks"
		case outcome == "none" && limited != "":
			why = "no check ran on its head: every one was skipped, and the host rate limited the deploy of " + limited
		case outcome == "none":
			why = "no check ran on its head: every one was skipped"
		case outcome == "pending":
			why = "its checks are still running"
		case outcome == "failed":
			why = "its checks failed"
		case isOutdated(owner, pr):
			why = "its goblin reported it done before its terminal last started and its checks finished after that report, so it rides once reported done again"
		default:
			if broke, found := brokeAt(past, pr.URL, pr.HeadRefOid); found {
				why = fmt.Sprintf("it broke train %s at this head, and rides again once its head changes", broke)
			}
		}
		if why != "" {
			left = append(left, fmt.Sprintf("#%d %s", pr.Number, why))
			continue
		}
		car := Car{Number: pr.Number, URL: pr.URL, Title: pr.Title, Branch: pr.HeadRefName, Head: pr.HeadRefOid, Task: owner.Task, Goblin: owner.Name, GoblinTitle: owner.Title, State: CarRiding}
		if limited != "" {
			car.Note = "the host rate limited the deploy of " + limited + ", which never built this head"
		}
		waiting = append(waiting, queued{car, done})
	}
	slices.SortStableFunc(waiting, func(a, b queued) int {
		return cmp.Or(a.done.Compare(b.done), cmp.Compare(a.car.Number, b.car.Number))
	})
	for _, next := range waiting {
		riders = append(riders, next.car)
	}
	return riders, left
}

// ownerOf is the goblin that reported url done, and when.
func ownerOf(goblins []Goblin, url string) (Goblin, time.Time) {
	for _, goblin := range goblins {
		if done, ok := goblin.Done[url]; ok {
			return goblin, done
		}
	}
	return Goblin{}, time.Time{}
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
