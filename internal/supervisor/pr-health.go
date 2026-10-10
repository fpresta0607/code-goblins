package supervisor

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/train"
)

// mergeabilityGrace is how long a head behind the default branch waits for
// GitHub to work out whether it conflicts.
const mergeabilityGrace = 10 * time.Minute

type reportedPRHealth struct {
	Head            string `json:"head"`
	HasBehindWake   bool   `json:"behind,omitempty"`
	HasConflictWake bool   `json:"conflicting,omitempty"`
	HasUnreadWake   bool   `json:"unread,omitempty"`
	// UnknownSince is when a poll first found GitHub had not yet worked out
	// whether the head conflicts, while it still has not.
	UnknownSince time.Time `json:"unknown_since,omitzero"`
	// RunningSince is when a poll first found the head's checks still
	// running, while they still are.
	RunningSince time.Time `json:"running_since,omitzero"`
	At           time.Time `json:"at"`
}

// unreadPRs is why a repository's pull request health was not all read on
// its last poll, and whether the CFO was woken for its listing reaching 100.
type unreadPRs struct {
	Failure    string `json:"failure"`
	HasCapWake bool   `json:"capped,omitempty"`
}

type prComparison struct {
	BehindBy   *int `json:"behindBy"`
	HeadTarget struct {
		Oid string `json:"oid"`
	} `json:"headTarget"`
	BaseTarget struct {
		Oid string `json:"oid"`
	} `json:"baseTarget"`
}

// comparePullRequests compares the head of each pull request in open, all
// listed by the checkout repo, with the default branch in one GraphQL
// request. It asks the repository the pull requests' own addresses name, as
// GitHub gave them, never one a remote of the checkout names. It returns the comparisons read, the heads whose own
// comparison came back missing or invalid in an otherwise readable response,
// and why anything was not read; a request that fails as a whole names no
// head.
func comparePullRequests(ctx context.Context, runner execx.Runner, repo string, open []ghPullRequest) (comparisons map[int]*prComparison, unread []ghPullRequest, branch string, err error) {
	comparisons = map[int]*prComparison{}
	if len(open) == 0 {
		return comparisons, nil, "", nil
	}
	branch, err = defaultBranch(ctx, runner, repo)
	if err != nil {
		return comparisons, nil, "", fmt.Errorf("PR health: compare the open pull requests of %s: %w", repo, err)
	}
	owner, name := pullRequestRepository(open[0].URL)
	var query strings.Builder
	fmt.Fprintf(&query, "query{repository(owner:%q,name:%q){ref(qualifiedName:%q){", owner, name, "refs/heads/"+branch)
	for _, pr := range open {
		fmt.Fprintf(&query, "pr%d:compare(headRef:%q){behindBy headTarget{oid} baseTarget{oid}}", pr.Number, pr.HeadRefOid)
	}
	query.WriteString("}}}")
	probe, cancel := context.WithTimeout(ctx, ghCallTimeout)
	defer cancel()
	result, err := runner.Run(probe, execx.Request{Dir: repo, Name: "gh", Args: []string{"api", "graphql", "--include", "-f", "query=" + query.String()}})
	if err != nil {
		return comparisons, nil, branch, fmt.Errorf("PR health: compare the open pull requests of %s: %w", repo, err)
	}
	_, body, err := githubResponse(result.Stdout)
	if err != nil {
		return comparisons, nil, branch, fmt.Errorf("PR health: cannot read the comparison response of %s: %w", repo, err)
	}
	var response struct {
		Data struct {
			Repository struct {
				Ref map[string]*prComparison `json:"ref"`
			} `json:"repository"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return comparisons, nil, branch, fmt.Errorf("PR health: cannot read the comparisons of %s: %w", repo, err)
	}
	var unreadable error
	for _, failure := range response.Errors {
		unreadable = errors.Join(unreadable, fmt.Errorf("PR health: compare %s: %s", repo, bounded(failure.Message, 300)))
	}
	if result.ExitCode != 0 {
		unreadable = errors.Join(unreadable, fmt.Errorf("PR health: compare %s: gh exited %d: %s", repo, result.ExitCode, bounded(strings.TrimSpace(string(result.Stderr)), 300)))
	}
	if response.Data.Repository.Ref == nil {
		return comparisons, nil, branch, errors.Join(unreadable, fmt.Errorf("PR health: GitHub returned no comparisons of %s against %s", repo, branch))
	}
	for _, pr := range open {
		comparison := response.Data.Repository.Ref[fmt.Sprintf("pr%d", pr.Number)]
		if comparison == nil || comparison.BehindBy == nil || *comparison.BehindBy < 0 || comparison.HeadTarget.Oid != pr.HeadRefOid || comparison.BaseTarget.Oid == "" {
			unreadable = errors.Join(unreadable, fmt.Errorf("PR health: comparison of %s at %s against %s was not read", pr.URL, pr.HeadRefOid, branch))
			unread = append(unread, pr)
			continue
		}
		comparisons[pr.Number] = comparison
	}
	return comparisons, unread, branch, unreadable
}

// prHealthChange is an open pull request whose head has fallen into a
// condition the CFO was not woken for: a conflict with its base, or a head
// behind the default branch, and the goblin whose pull request it is, if any.
type prHealthChange struct {
	pr            ghPullRequest
	goblin        string
	behind        int
	isConflicting bool
}

// healthChange says whether pr's head, as record last saw it, has fallen into
// a condition the CFO was not woken for. GitHub works out whether a head
// conflicts only once it is asked, and lists it UNKNOWN until then, so a
// behind head waits up to mergeabilityGrace for that answer: reporting it
// behind and then conflicting woke twice for one head that never changed.
// A behind head whose checks are still running waits for them too: no merge
// train has passed it over yet, and on 2026-10-10 one woke the CFO minutes
// after its push. It waits as long as a train waits for its own run,
// train.RunDeadline, so checks that never conclude do not hide it for ever.
func healthChange(record reportedPRHealth, pr ghPullRequest, behind int, now time.Time) (prHealthChange, bool) {
	isAwaitingGitHub := !record.UnknownSince.IsZero() && now.Sub(record.UnknownSince) < mergeabilityGrace
	isAwaitingChecks := !record.RunningSince.IsZero() && now.Sub(record.RunningSince) < train.RunDeadline
	isConflicting := pr.Mergeable == "CONFLICTING"
	if isConflicting && record.HasConflictWake || !isConflicting && (behind == 0 || record.HasBehindWake || isAwaitingGitHub || isAwaitingChecks) {
		return prHealthChange{}, false
	}
	return prHealthChange{pr: pr, behind: behind, isConflicting: isConflicting}, true
}

// reportPRHealth raises one pr_health wake for a poll's pull requests whose
// heads fell into a condition the CFO was not woken for, all of one
// repository, so a poll that finds dozens raises one wake, never one each.
// A pull request whose head and condition stay as they were is not named
// again. The wake waits out ciWakeGap after the repository's last one.
func reportPRHealth(stateDir string, w *fleetWakes, changes []prHealthChange, branch string, now time.Time) error {
	if len(changes) == 0 {
		return nil
	}
	owner, name := pullRequestRepository(changes[0].pr.URL)
	repository := owner + "/" + name
	key := "health:" + repository
	if !w.due(key, ciWakeGap, now) {
		return nil
	}
	var entries, goblins []string
	hasTeammates := false
	for _, change := range changes {
		pr := change.pr
		condition := fmt.Sprintf("is %d commits behind %s", change.behind, branch)
		if change.isConflicting {
			condition = "conflicts with its base " + cmp.Or(pr.BaseRefName, branch) + ", so its workflows cannot run"
		}
		whose := change.goblin
		if whose == "" {
			whose, hasTeammates = pr.Author.Login, true
		} else if !slices.Contains(goblins, whose) {
			goblins = append(goblins, whose)
		}
		entries = append(entries, fmt.Sprintf("%s's PR #%d (%s) at %s %s (%s)", whose, pr.Number, pr.HeadRefName, pr.HeadRefOid, condition, pr.URL))
	}
	detail := fmt.Sprintf("pr_health: %s: %s", repository, strings.Join(entries, "; "))
	if len(goblins) > 0 {
		detail += fmt.Sprintf("; next: tell %s to merge the default branch in with a merge commit, regenerate generated files rather than hand-merging them, run one CI run, and never force-push", strings.Join(goblins, " and "))
	}
	if hasTeammates {
		detail += "; the fleet reports a teammate's pull request and never pushes to it"
	}
	if err := raiseFleetWake(stateDir, "pr", key, detail); err != nil {
		return err
	}
	for _, change := range changes {
		record := w.Health[change.pr.URL]
		if change.isConflicting {
			record.HasConflictWake = true
		} else {
			record.HasBehindWake = true
		}
		w.Health[change.pr.URL] = record
	}
	w.woke(key, now)
	return nil
}

// reportPRUnread keeps why repo's pull request health was not all read until
// a poll reads it all, and raises pr_unread once for each condition: each
// open head whose own comparison was not read, and the listing reaching 100
// pull requests, again only after it cleared and came back. A comparison that
// failed as a whole only keeps its line.
func reportPRUnread(stateDir string, w *fleetWakes, repo string, heads []ghPullRequest, isCapped bool, failure error, now time.Time) error {
	if failure == nil {
		delete(w.PRUnread, repo)
		return nil
	}
	if w.PRUnread == nil {
		w.PRUnread = map[string]unreadPRs{}
	}
	record := w.PRUnread[repo]
	record.Failure = failure.Error()
	record.HasCapWake = record.HasCapWake && isCapped
	w.PRUnread[repo] = record
	isNewCap := isCapped && !record.HasCapWake
	key := "pr:unread:" + repo
	if len(heads) == 0 && !isNewCap || !w.due(key, ciWakeGap, now) {
		return nil
	}
	var conditions []string
	if len(heads) > 0 {
		named := make([]string, 0, len(heads))
		for _, pr := range heads {
			named = append(named, fmt.Sprintf("PR #%d at %s (%s)", pr.Number, pr.HeadRefOid, pr.URL))
		}
		conditions = append(conditions, fmt.Sprintf("%s could not be compared with the default branch; next: check each by hand with gh pr view <url> --json mergeable,mergeStateStatus; each head wakes again only when a later poll reads it conflicting or behind", strings.Join(named, ", ")))
	}
	if isNewCap {
		conditions = append(conditions, "the listing stopped at its limit of 100 open pull requests, so any further ones were not read; next: close or merge stale pull requests, or check the rest by hand with gh pr list --limit 200; this wakes again only after the listing falls under 100 and comes back")
	}
	detail := fmt.Sprintf("pr_unread: the health of %s's open pull requests was not all read: %s; why: %s; CI wakes and readable pull request health from this repository continue", filepath.Base(repo), strings.Join(conditions, "; and "), bounded(record.Failure, 300))
	if err := raiseFleetWake(stateDir, "pr", "repo:"+filepath.Base(repo), detail); err != nil {
		return err
	}
	for _, pr := range heads {
		health := w.Health[pr.URL]
		health.HasUnreadWake = true
		w.Health[pr.URL] = health
	}
	record.HasCapWake = isCapped
	w.PRUnread[repo] = record
	w.woke(key, now)
	return nil
}
