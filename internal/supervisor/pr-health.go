package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

type reportedPRHealth struct {
	Head            string    `json:"head"`
	HasBehindWake   bool      `json:"behind,omitempty"`
	HasConflictWake bool      `json:"conflicting,omitempty"`
	HasUnreadWake   bool      `json:"unread,omitempty"`
	At              time.Time `json:"at"`
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

func comparePullRequests(ctx context.Context, runner execx.Runner, repo string, open []ghPullRequest) (map[int]*prComparison, string, error) {
	comparisons := map[int]*prComparison{}
	if len(open) == 0 {
		return comparisons, "", nil
	}
	branch, err := defaultBranch(ctx, runner, repo)
	if err != nil {
		return comparisons, "", fmt.Errorf("PR health: compare the open pull requests of %s: %w", repo, err)
	}
	origin, err := runOutput(ctx, runner, repo, "git", "config", "--get", "remote.origin.url")
	if err != nil {
		return comparisons, branch, fmt.Errorf("PR health: compare the open pull requests of %s: %w", repo, err)
	}
	remote := githubRemote.FindStringSubmatch(origin)
	if remote == nil {
		return comparisons, branch, fmt.Errorf("PR health: cannot compare the non-GitHub origin of %s", repo)
	}
	owner, name, _ := strings.Cut(remote[1], "/")
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
		return comparisons, branch, fmt.Errorf("PR health: compare the open pull requests of %s: %w", repo, err)
	}
	_, body, err := githubResponse(result.Stdout)
	if err != nil {
		return comparisons, branch, fmt.Errorf("PR health: cannot read the comparison response of %s: %w", repo, err)
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
		return comparisons, branch, fmt.Errorf("PR health: cannot read the comparisons of %s: %w", repo, err)
	}
	var unreadable error
	for _, failure := range response.Errors {
		unreadable = errors.Join(unreadable, fmt.Errorf("PR health: compare %s: %s", repo, bounded(failure.Message, 300)))
	}
	if result.ExitCode != 0 {
		unreadable = errors.Join(unreadable, fmt.Errorf("PR health: compare %s: gh exited %d: %s", repo, result.ExitCode, bounded(strings.TrimSpace(string(result.Stderr)), 300)))
	}
	for _, pr := range open {
		comparison := response.Data.Repository.Ref[fmt.Sprintf("pr%d", pr.Number)]
		if comparison == nil || comparison.BehindBy == nil || *comparison.BehindBy < 0 || comparison.HeadTarget.Oid != pr.HeadRefOid || comparison.BaseTarget.Oid == "" {
			unreadable = errors.Join(unreadable, fmt.Errorf("PR health: comparison of %s at %s against %s was not read", pr.URL, pr.HeadRefOid, branch))
			continue
		}
		comparisons[pr.Number] = comparison
	}
	return comparisons, branch, unreadable
}

func reportPRHealth(stateDir string, w *fleetWakes, owner string, pr ghPullRequest, branch string, behind int, now time.Time) error {
	record := w.Health[pr.URL]
	isConflicting := pr.Mergeable == "CONFLICTING"
	if isConflicting && record.HasConflictWake || !isConflicting && (behind == 0 || record.HasBehindWake) || !w.due("health:"+pr.URL, ciWakeGap, now) {
		return nil
	}
	condition := fmt.Sprintf("is %d commits behind %s", behind, branch)
	if isConflicting {
		base := pr.BaseRefName
		if base == "" {
			base = branch
		}
		condition = "conflicts with its base " + base
	}
	key := pr.URL
	detail := fmt.Sprintf("pr_health: %s's PR #%d (%s) at %s %s (%s); the fleet reports it and never pushes to it", pr.Author.Login, pr.Number, pr.HeadRefName, pr.HeadRefOid, condition, pr.URL)
	if owner != "" {
		key = owner
		detail = fmt.Sprintf("pr_health: %s's PR #%d (%s) at %s %s (%s); next: tell %s to merge the default branch in with a merge commit, regenerate generated files rather than hand-merging them, run one CI run, and never force-push", owner, pr.Number, pr.HeadRefName, pr.HeadRefOid, condition, pr.URL, owner)
	}
	if err := raiseFleetWake(stateDir, "pr", key, detail); err != nil {
		return err
	}
	if isConflicting {
		record.HasConflictWake = true
	} else {
		record.HasBehindWake = true
	}
	w.Health[pr.URL] = record
	w.woke("health:"+pr.URL, now)
	return nil
}

// reportPRUnread keeps why repo's pull request health was not all read until
// a poll reads it all, and raises pr_unread once for each condition: each
// open head whose comparison was not read, and the listing reaching 100 pull
// requests, again only after it cleared and came back.
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
