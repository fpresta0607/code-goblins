package supervisor

import (
	"time"
)

// HostedChecks is what a pull request's hosted checks said at its head when
// the CI poll last read it, for its goblin's card. State is pending while a
// check runs and none has failed, failed as soon as one has, cancelled when
// they ended with one cancelled and none failed, and passed when every one
// passed. Failed names the checks that failed or were cancelled, and Link is
// the first one's page. Approved says a reviewer approved the pull request.
type HostedChecks struct {
	Head     string    `json:"head"`
	State    string    `json:"state"`
	Checks   int       `json:"checks"`
	Failed   []string  `json:"failed,omitempty"`
	Link     string    `json:"link,omitempty"`
	Approved bool      `json:"approved,omitempty"`
	At       time.Time `json:"at"`
}

// recordHostedChecks keeps what pr's checks say now, or forgets pr when it has
// none, which leaves nothing for its card to show.
func recordHostedChecks(w *fleetWakes, pr ghPullRequest, now time.Time) {
	if len(pr.Checks) == 0 {
		delete(w.Hosted, pr.URL)
		return
	}
	hosted := HostedChecks{Head: pr.HeadRefOid, Checks: len(pr.Checks), Approved: pr.ReviewDecision == "APPROVED", At: now}
	var running, failed, cancelled bool
	for _, check := range pr.Checks {
		switch {
		case !check.concluded():
			running = true
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
		hosted.State = "pending"
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
