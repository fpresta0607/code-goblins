package supervisor

import (
	"regexp"
	"slices"
	"strings"
	"time"
)

// Deployment is how the deploy of a merged pull request stands: the newest
// runs of the workflows on the default branch whose names say deploy, for the
// commit its merge pushed, as the CI poll last read them, a skipped run apart.
// State is deploying while one runs and none has failed, failed as soon as
// one has, cancelled when they ended with one cancelled and none failed, and
// deployed when every one succeeded. Link is the page of the first run that
// failed or was cancelled, else of the newest run, and Workflows names them.
type Deployment struct {
	Commit    string    `json:"commit"`
	State     string    `json:"state"`
	Workflows []string  `json:"workflows"`
	Link      string    `json:"link"`
	At        time.Time `json:"at"`
}

// mergedPullRequest finds the pull request a push run's commit merged in its
// title: a merge commit's "Merge pull request #N from ..." or a squash's
// "... (#N)".
var mergedPullRequest = regexp.MustCompile(`^Merge pull request #(\d+) from |\(#(\d+)\)$`)

// runRepository is the repository page a run's own page sits under.
var runRepository = regexp.MustCompile(`^(https://github\.com/[^/\s]+/[^/\s]+)/actions/runs/\d+`)

// recordDeployments keeps, for each pull request whose merge a deploy
// workflow on the default branch ran for, how its newest commit's deploy
// stands. Runs come newest first, so the first commit a pull request's runs
// name is its newest, and each workflow's first run of it is its newest.
func recordDeployments(w *fleetWakes, runs []ghRun, now time.Time) {
	type reading struct {
		deployment                 Deployment
		running, failed, cancelled bool
		problem                    string
	}
	read := map[string]*reading{}
	var order []string
	for _, run := range runs {
		// A run skipped, as a deploy whose condition did not hold is,
		// deployed nothing.
		if !strings.Contains(strings.ToLower(run.Workflow), "deploy") || run.Conclusion == "skipped" {
			continue
		}
		match := mergedPullRequest.FindStringSubmatch(run.Title)
		repository := runRepository.FindStringSubmatch(run.URL)
		if match == nil || repository == nil {
			continue
		}
		url := repository[1] + "/pull/" + match[1] + match[2]
		r := read[url]
		if r == nil {
			r = &reading{deployment: Deployment{Commit: run.HeadSHA, Link: run.URL, At: now}}
			read[url] = r
			order = append(order, url)
		}
		if run.HeadSHA != r.deployment.Commit || slices.Contains(r.deployment.Workflows, run.Workflow) {
			continue
		}
		r.deployment.Workflows = append(r.deployment.Workflows, run.Workflow)
		switch {
		case run.Status != "completed":
			r.running = true
			continue
		case run.Conclusion == "success":
			continue
		case run.Conclusion == "cancelled":
			r.cancelled = true
		default:
			r.failed = true
		}
		if r.problem == "" {
			r.problem = run.URL
		}
	}
	if len(order) > 0 && w.Deploys == nil {
		w.Deploys = map[string]Deployment{}
	}
	for _, url := range order {
		r := read[url]
		switch {
		case r.failed:
			r.deployment.State = "failed"
		case r.running:
			r.deployment.State = "deploying"
		case r.cancelled:
			r.deployment.State = "cancelled"
		default:
			r.deployment.State = "deployed"
		}
		if r.problem != "" {
			r.deployment.Link = r.problem
		}
		w.Deploys[url] = r.deployment
	}
}
