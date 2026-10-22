package supervisor

import (
	"slices"
	"time"
)

// HostedChecks is what a pull request's hosted checks said at its head when
// the CI poll last read it, for its goblin's card. State is pending while a
// check runs and none has failed, failed as soon as one has, cancelled when
// they ended with one cancelled and none failed, and passed when every one
// passed. Failed names the checks that failed or were cancelled, and Link is
// the first one's page, or while they are pending the first running one's,
// the run that tests the pull request. Approved says a reviewer approved the
// pull request. RateLimited names the deploys the host rate limited, which
// never built the head and count as not run, so Checks leaves them out.
type HostedChecks struct {
	Head        string    `json:"head"`
	State       string    `json:"state"`
	Checks      int       `json:"checks"`
	Failed      []string  `json:"failed,omitempty"`
	RateLimited []string  `json:"rate_limited,omitempty"`
	Link        string    `json:"link,omitempty"`
	Approved    bool      `json:"approved,omitempty"`
	At          time.Time `json:"at"`
}

// recordHostedChecks keeps what pr's checks say now, or forgets pr when it has
// none, which leaves nothing for its card to show. Checks that all concluded
// skipped or neutral are none: a draft's run skips its jobs, and they tested
// nothing. So are those a deploy the host rate limited joins.
func recordHostedChecks(w *fleetWakes, pr ghPullRequest, now time.Time) {
	if !slices.ContainsFunc(pr.Checks, func(check ghCheck) bool { return !check.concluded() || check.hasRun() }) {
		delete(w.Hosted, pr.URL)
		return
	}
	hosted := HostedChecks{Head: pr.HeadRefOid, Checks: len(pr.Checks), Approved: pr.ReviewDecision == "APPROVED", At: now}
	var running, failed, cancelled bool
	var runningLink string
	for _, check := range pr.Checks {
		switch {
		case !check.concluded():
			running = true
			if runningLink == "" {
				runningLink = check.link()
			}
			continue
		case check.rateLimited():
			hosted.Checks--
			hosted.RateLimited = append(hosted.RateLimited, check.name())
			continue
		case check.passed():
			continue
		case check.outcome() == "CANCELLED":
			cancelled = true
		default:
			failed = true
		}
		hosted.Failed = append(hosted.Failed, check.name())
		if hosted.Link == "" {
			hosted.Link = check.link()
		}
	}
	switch {
	case failed:
		hosted.State = "failed"
	case running:
		hosted.State, hosted.Link = "pending", runningLink
	case cancelled:
		hosted.State = "cancelled"
	default:
		hosted.State = "passed"
	}
	if w.Hosted == nil {
		w.Hosted = map[string]HostedChecks{}
	}
	w.Hosted[pr.URL] = hosted
}
