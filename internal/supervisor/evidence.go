package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/crewstate"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// The board reads the fleet's own records for work no native hook reported:
// task metadata, status logs, the wake queue, the gate database, merge
// commits in fleet repositories and what cfo cleanup leaves behind. Without
// them a goblin that never installed the native hooks read as awaiting
// evidence however busy it was.

const (
	historyWindow      = 7 * 24 * time.Hour
	historyLimit       = 20
	pullRequestRecheck = 10 * time.Minute
	pullHeadsRecheck   = 10 * time.Minute
	originRecheck      = 30 * time.Minute
	pullRequestBudget  = 5 * time.Second
	// keepHistory rebuilds the Completed column every historyRefresh, and
	// sooner when what it watches every historyWatch changes.
	historyRefresh = time.Minute
	historyWatch   = 10 * time.Second
)

var (
	archivedStatusFile = regexp.MustCompile(`^(.+)\.status\.(\d{8}T\d{6}Z)$`)
	archivedTaskDir    = regexp.MustCompile(`^(.+)\.(\d{8}T\d{6}Z)$`)
	mergeSubject       = regexp.MustCompile(`^Merge pull request #(\d+) from [^/\s]+/(\S+)`)
	githubRemote       = regexp.MustCompile(`^(?:https://github\.com/|git@github\.com:)([\w.-]+/[\w.-]+?)(?:\.git)?/?$`)
	githubPullRequest  = regexp.MustCompile(`^https://github\.com/[\w.-]+/[\w.-]+/pull/\d+$`)
)

// pullRequestState is what GitHub last answered for a pull request, and when:
// OPEN, CLOSED or MERGED, or empty when it did not answer.
type pullRequestState struct {
	state string
	title string
	at    time.Time
}

type PullRequestInfo struct {
	State string `json:"state"`
	Title string `json:"title"`
}

// MergedPR is one pull request merged into a fleet repository.
type MergedPR struct {
	PR      string
	Branch  string
	Project string
	At      int64
}

// FleetRepos are the repositories whose merges the board lists: this home
// and every git checkout directly under the projects root.
func FleetRepos(h home.Home, projectsRoot string) []string {
	repos := []string{h.Root}
	if entries, err := os.ReadDir(projectsRoot); err == nil && projectsRoot != "" {
		for _, entry := range entries {
			dir := filepath.Join(projectsRoot, entry.Name())
			if entry.IsDir() && exists(filepath.Join(dir, ".git")) && !slicesContainsPath(repos, dir) {
				repos = append(repos, dir)
			}
		}
	}
	return repos
}

func slicesContainsPath(paths []string, path string) bool {
	for _, p := range paths {
		if fsx.SamePath(p, path) {
			return true
		}
	}
	return false
}

// GitMergedPRs reads merged pull requests from each repository's own
// history. The fleet merges with merge commits, whose subject names the pull
// request, so this sees every merge, gated or not, without a forge call; the
// gate database only knows merges its CI monitor happened to watch. A merge
// commit several repositories hold, such as a fork's copy of its upstream's
// history, is listed once, under the repository the pull request was opened
// in (see gitMergedPRs).
func GitMergedPRs(repos []string) func(context.Context, time.Time) ([]MergedPR, error) {
	return gitMergedPRs(repos, pullRequestHeads, time.Now)
}

// gitMergedPRs lists merges as GitMergedPRs does. A merge commit more than one
// repository holds goes to the repository whose origin publishes that pull
// request's head as the merge's second parent; when none does, to the first
// repository listed, the CFO home first. heads lists every pull request head
// a repository's origin publishes in one call, and a listing is kept: it is
// asked for again only for a pull request it does not list or lists with
// another head, such as one listed while it was still open, and then at most
// every pullHeadsRecheck, so a fork carrying a week of its upstream's merges
// costs one call per repository, not one per merge. A checkout is read with
// git only when refStamp says a fetch or a remote change rewrote its git
// files; until then the scan uses the merges it read last, so a refresh with
// nothing new runs no git at all.
func gitMergedPRs(repos []string, heads func(ctx context.Context, repo string) (map[string]string, error), now func() time.Time) func(context.Context, time.Time) ([]MergedPR, error) {
	type listing struct {
		heads map[string]string
		at    time.Time
	}
	listings := map[string]listing{}
	type merge struct {
		pr                   MergedPR
		commit, number, head string
	}
	// checkout is what a repository gave when it was last read: its git
	// directory (empty when git could not read it), the stamp of its git
	// files then, the window it was read for and the merges in it.
	type checkout struct {
		common, stamp string
		read, since   time.Time
		merges        []merge
	}
	checkouts := map[string]checkout{}
	return func(ctx context.Context, since time.Time) ([]MergedPR, error) {
		var errs error
		publishes := func(repo, number, head string) bool {
			known, ok := listings[repo]
			if !ok || known.heads[number] != head && now().Sub(known.at) >= pullHeadsRecheck {
				fresh, err := heads(ctx, repo)
				errs = errors.Join(errs, err)
				if err == nil {
					known.heads = fresh
				}
				known.at = now()
				listings[repo] = known
			}
			return known.heads[number] == head
		}
		read := func(repo string) (checkout, error) {
			known := checkout{read: now(), since: since}
			common, err := (Git{}).run(ctx, repo, "rev-parse", "--path-format=absolute", "--git-common-dir")
			if err != nil {
				return known, nil // Not a checkout git can read now; it is tried again after originRecheck.
			}
			known.common = strings.TrimSpace(common)
			// The stamp is taken first, so a fetch during the read is seen next time.
			known.stamp = refStamp(known.common)
			remote, err := (Git{}).run(ctx, repo, "remote", "get-url", "origin")
			match := githubRemote.FindStringSubmatch(strings.TrimSpace(remote))
			if err != nil || match == nil {
				return known, nil // Only a GitHub remote gives a pull request a link.
			}
			ref := ""
			for _, candidate := range []string{"origin/HEAD", "origin/main", "origin/master"} {
				if _, err := (Git{}).run(ctx, repo, "rev-parse", "--verify", "--quiet", "refs/remotes/"+candidate); err == nil {
					ref = candidate
					break
				}
			}
			if ref == "" {
				return known, nil
			}
			out, err := (Git{}).run(ctx, repo, "log", ref, "--merges", "--since="+since.UTC().Format(time.RFC3339), "--format=%H%x09%P%x09%ct%x09%s")
			if err != nil {
				return checkout{}, err
			}
			for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
				fields := strings.SplitN(strings.TrimSpace(line), "\t", 4)
				if len(fields) != 4 {
					continue
				}
				parents := strings.Fields(fields[1])
				m := mergeSubject.FindStringSubmatch(fields[3])
				seconds, err := strconv.ParseInt(fields[2], 10, 64)
				if len(parents) < 2 || m == nil || err != nil {
					continue
				}
				known.merges = append(known.merges, merge{pr: MergedPR{PR: "https://github.com/" + match[1] + "/pull/" + m[1], Branch: m[2], Project: repo, At: seconds}, commit: fields[0], number: m[1], head: parents[1]})
			}
			return known, nil
		}
		holders := map[string][]merge{}
		var commits []string
		for _, repo := range repos {
			known, ok := checkouts[repo]
			current := ok && !since.Before(known.since) && (known.common != "" && refStamp(known.common) == known.stamp || known.common == "" && now().Sub(known.read) < originRecheck)
			if !current {
				var err error
				if known, err = read(repo); err != nil {
					errs = errors.Join(errs, err)
					delete(checkouts, repo)
					continue
				}
				checkouts[repo] = known
			}
			for _, found := range known.merges {
				if found.pr.At < since.Unix() {
					continue
				}
				if _, seen := holders[found.commit]; !seen {
					commits = append(commits, found.commit)
				}
				holders[found.commit] = append(holders[found.commit], found)
			}
		}
		merged := make([]MergedPR, 0, len(commits))
		for _, commit := range commits {
			candidates := holders[commit]
			chosen := candidates[0]
			if len(candidates) > 1 {
				for _, candidate := range candidates {
					if publishes(candidate.pr.Project, candidate.number, candidate.head) {
						chosen = candidate
						break
					}
				}
			}
			merged = append(merged, chosen.pr)
		}
		return merged, errs
	}
}

// refStamp is the size and time of each file a fetch or a remote change
// rewrites in a git directory: its config, packed refs, and origin's HEAD,
// main and master refs, with the ref origin's HEAD names. While the stamp
// holds, the checkout's merges are the ones last read.
func refStamp(common string) string {
	names := []string{"config", "packed-refs", "refs/remotes/origin/HEAD", "refs/remotes/origin/main", "refs/remotes/origin/master"}
	if head, err := os.ReadFile(filepath.Join(common, "refs", "remotes", "origin", "HEAD")); err == nil {
		if target, ok := strings.CutPrefix(strings.TrimSpace(string(head)), "ref: "); ok {
			names = append(names, target)
		}
	}
	var stamp strings.Builder
	for _, name := range names {
		if info, err := os.Stat(filepath.Join(common, filepath.FromSlash(name))); err == nil {
			fmt.Fprintf(&stamp, "%d:%d;", info.Size(), info.ModTime().UnixNano())
		} else {
			stamp.WriteString("-;")
		}
	}
	return stamp.String()
}

// pullRequestHeads lists the head commit of every pull request repo's origin
// publishes, by number, as GitHub publishes them: refs/pull/<number>/head.
func pullRequestHeads(ctx context.Context, repo string) (map[string]string, error) {
	out, err := (Git{}).run(ctx, repo, "ls-remote", "origin", "refs/pull/*/head")
	if err != nil {
		return nil, err
	}
	heads := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		head, ref, ok := strings.Cut(strings.TrimSpace(line), "\t")
		number, isHead := strings.CutSuffix(strings.TrimPrefix(ref, "refs/pull/"), "/head")
		if ok && isHead && number != "" {
			heads[number] = head
		}
	}
	return heads, nil
}

// fleetEvaluation is the status of a task no native hook has reported: a
// question it is still waiting on first, then what the gate proved, then what
// Herdr sees in its pane.
func fleetEvaluation(evaluation Evaluation, meta state.TaskMeta, runtime RuntimeEvidence, records []wake.Record) Evaluation {
	if evaluation.Generation != meta.SpawnGen {
		evaluation = Evaluation{Generation: meta.SpawnGen}
	}
	if verb, detail, ok := waitingQuestion(records, meta.ID); ok {
		evaluation.Phase, evaluation.Reason = verb, "Waiting on the CFO: "+detail
		return evaluation
	}
	switch evaluation.Phase {
	case "blocked", "ready", "merged", "done":
		return evaluation
	}
	switch {
	case runtime.working():
		evaluation.Phase, evaluation.Reason = "working", runtime.Reason
	case runtime.State == string(monitor.HealthIdle) || runtime.State == string(monitor.HealthParked):
		evaluation.Phase, evaluation.Reason = "idle", runtime.Reason
	case evaluation.Phase == "":
		evaluation.Phase, evaluation.Reason = "unknown", runtime.Reason
	}
	return evaluation
}

// waitingQuestion is the blocked or failed notify a task is still waiting on;
// one the Overlord answered on the board no longer holds the goblin.
func waitingQuestion(records []wake.Record, id string) (string, string, bool) {
	for i := len(records) - 1; i >= 0; i-- {
		if verb, ok := wake.BlockingNotify(records[i]); ok && records[i].Key == id && records[i].Answered == "" {
			return verb, strings.TrimSpace(strings.TrimPrefix(records[i].Detail, verb+":")), true
		}
	}
	return "", "", false
}

// latestReport is the last line a task's current generation wrote to its
// status log, with when it was written.
func latestReport(lines []string, spawned time.Time) (time.Time, string) {
	for i := len(lines) - 1; i >= 0; i-- {
		stamp, event := state.SplitStatus(lines[i])
		if !spawned.IsZero() && stamp.Before(spawned.Truncate(time.Second)) {
			break
		}
		if event = strings.TrimSpace(event); event != "" && !crewstate.IsCFOAudit(event) {
			return stamp, event
		}
	}
	return time.Time{}, ""
}

// standingReport is latestReport passed over the questions the goblin asked
// since: a question waits beside what the goblin stands on, such as a wait on
// the Overlord, and replaces nothing.
func standingReport(lines []string, spawned time.Time) (time.Time, string) {
	return latestReport(slices.DeleteFunc(slices.Clone(lines), func(line string) bool {
		_, event := state.SplitStatus(line)
		return strings.HasPrefix(strings.TrimSpace(event), "blocked: ")
	}), spawned)
}

// reportKind is what a goblin's latest report says about it: working,
// blocked, failed, done or waiting, or empty for anything else, so the board
// can tell a goblin's own failure or finish from routine news.
func reportKind(report string) string {
	for _, kind := range []string{"working", "blocked", "failed", "done"} {
		if strings.HasPrefix(report, kind+": ") {
			return kind
		}
	}
	if strings.HasPrefix(report, "waiting on ") {
		return "waiting"
	}
	return ""
}

// supersedesQuestion says a report is newer news than a question the task
// asked before it: the goblin went back to work or waits on something. The
// question stays in the CFO's queue; only the board's reading of the task
// changes.
func supersedesQuestion(report string) bool {
	return strings.HasPrefix(report, "working: ") || strings.HasPrefix(report, "waiting on ")
}

// reportedProgress is what a goblin last said it is doing: working on
// something, or waiting on another task, the Overlord, CI or a deploy. A wait
// on another task clears itself once that task reports done, and a wait on the
// Overlord once the Command Center item it raised closed, answered, cleared or
// handed to the CFO; any other wait lasts until the goblin reports again.
func reportedProgress(stateDir, id string, reviews []Review, reportedAt time.Time, report string) (phase, reason, waitingOn string, ok bool) {
	if what, found := strings.CutPrefix(report, "working: "); found {
		return "working", what, "", true
	}
	rest, found := strings.CutPrefix(report, "waiting on ")
	target, why, separated := strings.Cut(rest, ": ")
	if !found || !separated {
		return "", "", "", false
	}
	switch target {
	case "ci", "deploy":
	case "overlord":
		// Its item is published after the report. An answer typed on the item
		// counts once it reached the goblin; until then it is still on its way.
		// An answer he gave elsewhere, such as on the item's page, is the
		// CFO's to relay.
		if slices.ContainsFunc(reviews, func(r Review) bool {
			return r.Task == id && strings.HasPrefix(r.ID, "waiting-"+id+"-") && !r.CreatedAt.Before(reportedAt) &&
				r.State != "open" && (r.State != "answered" || r.Delivered || r.AnsweredIn != "")
		}) {
			return "", "", "", false
		}
	default:
		lines, _ := state.TailStatus(stateDir, target, 200)
		for i := len(lines) - 1; i >= 0; i-- {
			stamp, event := state.SplitStatus(lines[i])
			if stamp.Before(reportedAt) {
				break
			}
			if strings.HasPrefix(strings.TrimSpace(event), "done: ") {
				return "", "", "", false
			}
		}
	}
	return "waiting", why, target, true
}

// statusActivity is a task's own latest status line, the one-line answer to
// what it is doing now, and the pull request it last reported done. A known
// spawn time limits both to lines its own generation wrote, because a reused
// task id appends to the log its earlier generation left.
func statusActivity(lines []string, spawned time.Time) (string, string) {
	activity, pr := "", ""
	for i := len(lines) - 1; i >= 0; i-- {
		stamp, event := state.SplitStatus(lines[i])
		if !spawned.IsZero() && stamp.Before(spawned.Truncate(time.Second)) {
			break
		}
		event = strings.TrimSpace(event)
		if activity == "" && !crewstate.IsCFOAudit(event) {
			activity = event
		}
		// Only an https link is a pull request; the line is goblin-written.
		if rest, ok := strings.CutPrefix(event, "done: PR "); ok && pr == "" {
			if fields := strings.Fields(rest); len(fields) > 0 && strings.HasPrefix(fields[0], "https://") {
				pr = fields[0]
			}
		}
		if activity != "" && pr != "" {
			break
		}
	}
	// The panel shows the whole line behind Show more, so only a runaway
	// line is cut.
	return bounded(activity, 4000), pr
}

func taskSessionSummary(lines []string, spawned time.Time) (report string, retired time.Time) {
	for i := len(lines) - 1; i >= 0; i-- {
		stamp, event := state.SplitStatus(lines[i])
		if !spawned.IsZero() && stamp.Before(spawned.Truncate(time.Second)) {
			break
		}
		event = strings.TrimSpace(event)
		kind, detail, _ := strings.Cut(event, ": ")
		if (kind == "done" || kind == "stopped") && (strings.HasPrefix(detail, "returned worktree ") || strings.HasPrefix(detail, "force-archived via cfo cleanup")) {
			if retired.IsZero() && report == "" {
				retired = stamp
			}
		} else if report == "" && reportKind(event) != "" {
			report = redact(bounded(event, 4000))
		}
	}
	return report, retired
}

// finishedTasks are tasks cfo cleanup finished within the history window,
// newest first. Cleanup leaves the status log in place with no task record
// beside it; cfo reap later moves it into the archive as its own file, and
// older archives keep it inside the task's archived directory.
func finishedTasks(h home.Home, now time.Time) []Task {
	stateDir := h.State
	type finished struct {
		at   time.Time
		path string
	}
	found := map[string]finished{}
	keep := func(id, path string, at time.Time) {
		if state.ValidTaskID(id) != nil || now.Sub(at) > historyWindow {
			return
		}
		if prior, ok := found[id]; !ok || at.After(prior.at) {
			found[id] = finished{at, path}
		}
	}
	if entries, err := os.ReadDir(stateDir); err == nil {
		for _, entry := range entries {
			id, ok := strings.CutSuffix(entry.Name(), ".status")
			if !ok || entry.IsDir() {
				continue
			}
			if _, err := os.Stat(filepath.Join(stateDir, id+".meta")); err == nil {
				continue
			}
			if info, err := entry.Info(); err == nil {
				keep(id, filepath.Join(stateDir, entry.Name()), info.ModTime())
			}
		}
	}
	archive := filepath.Join(stateDir, state.ArchiveDirName)
	if entries, err := os.ReadDir(archive); err == nil {
		for _, entry := range entries {
			if m := archivedStatusFile.FindStringSubmatch(entry.Name()); m != nil && !entry.IsDir() {
				if at, err := time.Parse("20060102T150405Z", m[2]); err == nil {
					keep(m[1], filepath.Join(archive, entry.Name()), at)
				}
			} else if m := archivedTaskDir.FindStringSubmatch(entry.Name()); m != nil && entry.IsDir() {
				path := filepath.Join(archive, entry.Name(), m[1]+".status")
				if at, err := time.Parse("20060102T150405Z", m[2]); err == nil && exists(path) {
					keep(m[1], path, at)
				}
			}
		}
	}
	tasks := []Task{}
	backlog, _ := fleet.ReadBacklog(h)
	for id, f := range found {
		lines, err := fsx.ReadLines(f.path)
		if err != nil {
			continue
		}
		_, pr := statusActivity(lines, time.Time{})
		report, retired := taskSessionSummary(lines, time.Time{})
		phase, reason := "stopped", "Stopped without recorded delivery"
		if pr != "" {
			phase, reason = "done", "Delivered pull request; task cleaned up"
		}
		title, project := id, ""
		for _, row := range append(append(backlog.Done, backlog.Queued...), backlog.Parked...) {
			if row.Structured && row.ID == id {
				title, project = row.Title, row.Repo
				break
			}
		}
		for _, brief := range []string{filepath.Join(h.Data, id, "brief.md"), filepath.Join(h.Data, "archive", "finished", id, "brief.md")} {
			if project == "" {
				project = briefProject(brief)
			}
			if title == id {
				if content, err := os.ReadFile(brief); err == nil {
					_, task, ok := strings.Cut(strings.ReplaceAll(string(content), "\r\n", "\n"), "## Task\n")
					if ok {
						first, _, _ := strings.Cut(strings.TrimSpace(task), "\n")
						if !strings.HasPrefix(first, "#") && first != "" {
							title = bounded(first, 200)
						}
					}
				}
			}
		}
		if project != "" {
			project = filepath.Base(project)
		}
		tasks = append(tasks, Task{ID: "finished:" + id, Title: title, Project: project, Dependencies: []string{}, Archived: true, LastReport: report, RetiredAt: retired, Evaluation: Evaluation{Phase: phase, PR: pr, Reason: reason, At: f.at}})
	}
	for _, directory := range []string{"outcomes", "lifecycle"} {
		entries, _ := os.ReadDir(filepath.Join(stateDir, directory))
		for _, entry := range entries {
			id, ok := strings.CutSuffix(entry.Name(), ".json")
			if !ok || state.ValidTaskID(id) != nil || exists(filepath.Join(stateDir, id+".meta")) {
				continue
			}
			var task Task
			var generation string
			if directory == "outcomes" {
				outcome, err := state.ReadOutcome(stateDir, id)
				if err != nil {
					continue
				}
				generation = outcome.Generation
				task = Task{ID: "finished:" + id, Title: outcome.Title, Project: filepath.Base(outcome.Project), Branch: outcome.Branch, Archived: true, Dependencies: []string{}, Evaluation: Evaluation{Phase: outcome.Phase, PR: outcome.PR, Reason: outcome.Reason, At: outcome.At}}
			} else {
				record, err := state.ReadLifecycle(stateDir, id)
				if err != nil || record.Phase != "stopped" {
					continue
				}
				generation = record.Generation
				task = Task{ID: "finished:" + id, Title: record.Title, Project: filepath.Base(record.Project), Archived: true, Dependencies: []string{}, Lifecycle: lifecycleStatus(record), Teardown: record.TeardownLabels(), Evaluation: Evaluation{Phase: "stopped", Reason: record.Reason, At: record.Updated}}
				if at := slices.IndexFunc(tasks, func(existing Task) bool { return existing.ID == task.ID }); at >= 0 {
					task.PR, task.Branch = tasks[at].PR, tasks[at].Branch
				}
			}
			if task.Title == "" {
				task.Title = id
			}
			if task.Project == "." {
				task.Project = ""
			}
			if now.Sub(task.At) > historyWindow {
				continue
			}
			if status, ok := found[id]; ok && generation != "queued" {
				if lines, err := fsx.ReadLines(status.path); err == nil {
					task.LastReport, task.RetiredAt = taskSessionSummary(lines, spawnTime(generation))
				}
			}
			tasks = slices.DeleteFunc(tasks, func(existing Task) bool { return existing.ID == task.ID })
			tasks = append(tasks, task)
		}
	}
	return newestHistory(tasks)
}

// withMergedPRs adds the merged pull requests to the finished
// tasks: a finished task that reported the same pull request carries the
// merge, and any other merged pull request is its own completed entry.
func withMergedPRs(history []Task, merged []MergedPR) []Task {
	for _, pr := range merged {
		found := false
		for i := range history {
			if history[i].PR == pr.PR {
				history[i].Merged, found = true, true
				history[i].Project, history[i].Branch = filepath.Base(pr.Project), pr.Branch
			}
		}
		if !found {
			history = append(history, Task{ID: "merged:" + pr.PR, Title: "Pull request #" + filepath.Base(pr.PR), Branch: pr.Branch, Project: filepath.Base(pr.Project), Dependencies: []string{}, Archived: true, Merged: true, Evaluation: Evaluation{Phase: "done", PR: pr.PR, Reason: "Pull request merged", At: time.Unix(pr.At, 0).UTC()}})
		}
	}
	return newestHistory(history)
}

// withPullRequestStates asks GitHub about each finished task's pull request
// that no fleet history shows merged, and marks it merged (a squash merge
// leaves no merge commit) or closed without merging. A merged or closed pull
// request is not asked about again; an open one, or one GitHub did not
// answer for, waits pullRequestRecheck. All asks of one refresh share
// pullRequestBudget, so a slow GitHub never holds the supervisor's loop much
// longer; a pull request not asked before it runs out is asked on the next
// refresh.
func (s *Service) withPullRequestStates(ctx context.Context, history []Task, now time.Time) error {
	if s.Options.PullRequestState == nil {
		return nil
	}
	budget, cancel := context.WithTimeout(ctx, pullRequestBudget)
	defer cancel()
	if s.pullRequests == nil {
		s.pullRequests = map[string]pullRequestState{}
	}
	var errs error
	shown := map[string]bool{}
	for i := range history {
		task := &history[i]
		if !githubPullRequest.MatchString(task.PR) {
			continue
		}
		shown[task.PR] = true
		known, ok := s.pullRequests[task.PR]
		if !ok || (known.state != "MERGED" && known.state != "CLOSED" || known.title == "") && now.Sub(known.at) >= pullRequestRecheck {
			if budget.Err() != nil {
				continue
			}
			answer, err := s.Options.PullRequestState(budget, task.PR)
			errs = errors.Join(errs, err)
			known = pullRequestState{state: answer.State, title: answer.Title, at: now}
			s.pullRequests[task.PR] = known
		}
		task.Merged, task.Closed = task.Merged || known.state == "MERGED", known.state == "CLOSED"
		if known.title != "" {
			task.Title = known.title
		}
		parts := strings.Split(task.PR, "/")
		task.Project = parts[4]
	}
	maps.DeleteFunc(s.pullRequests, func(url string, _ pullRequestState) bool { return !shown[url] })
	return errs
}

// GitHubPullRequestState asks GitHub, through gh, whether a pull request is
// OPEN, CLOSED or MERGED, for as long as the caller's context allows.
func GitHubPullRequestState(commands execx.Runner) func(context.Context, string) (PullRequestInfo, error) {
	return func(ctx context.Context, url string) (PullRequestInfo, error) {
		result, err := commands.Run(ctx, execx.Request{Name: "gh", Args: []string{"pr", "view", url, "--json", "state,title"}})
		if err != nil {
			return PullRequestInfo{}, fmt.Errorf("gh could not read %s: %w", url, err)
		}
		if result.ExitCode != 0 {
			return PullRequestInfo{}, fmt.Errorf("gh could not read %s: %s", url, strings.TrimSpace(string(result.Stderr)))
		}
		var answer PullRequestInfo
		if err := json.Unmarshal(result.Stdout, &answer); err != nil || strings.TrimSpace(answer.Title) == "" {
			return PullRequestInfo{}, errors.New("gh returned incomplete pull request details")
		}
		switch answer.State {
		case "OPEN", "CLOSED", "MERGED":
			return answer, nil
		default:
			return PullRequestInfo{}, fmt.Errorf("gh answered %q for the state of %s", answer.State, url)
		}
	}
}

// spawnTime is when a task generation started, which its spawn generation
// records as s followed by Unix nanoseconds; zero when it does not.
func spawnTime(generation string) time.Time {
	digits, ok := strings.CutPrefix(generation, "s")
	nanos, err := strconv.ParseInt(digits, 10, 64)
	if !ok || err != nil {
		return time.Time{}
	}
	return time.Unix(0, nanos).UTC()
}

func newestHistory(tasks []Task) []Task {
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].At.After(tasks[j].At) })
	if len(tasks) > historyLimit {
		tasks = tasks[:historyLimit]
	}
	return tasks
}

// queuedBriefs are briefs nothing has started: no live task record, and no
// status log or archive entry, which every dispatched task leaves.
func queuedBriefs(h home.Home) []Task {
	entries, err := os.ReadDir(h.Data)
	if err != nil {
		return nil
	}
	archived, _ := os.ReadDir(filepath.Join(h.State, state.ArchiveDirName))
	tasks := []Task{}
	for _, entry := range entries {
		id := entry.Name()
		if !entry.IsDir() || state.ValidTaskID(id) != nil || !exists(filepath.Join(h.Data, id, "brief.md")) {
			continue
		}
		if exists(filepath.Join(h.State, id+".meta")) || exists(filepath.Join(h.State, id+".status")) {
			continue
		}
		dispatched := false
		for _, a := range archived {
			if strings.HasPrefix(a.Name(), id+".") {
				dispatched = true
				break
			}
		}
		if !dispatched {
			project := briefProject(filepath.Join(h.Data, id, "brief.md"))
			if project != "" {
				project = filepath.Base(project)
			}
			tasks = append(tasks, Task{ID: id, Title: id, Project: project, Dependencies: []string{}, Since: briefWritten(h, id), Evaluation: Evaluation{Phase: "queued", Reason: "Brief ready at data/" + id + "/brief.md; not dispatched yet"}})
		}
	}
	return tasks
}

// sessionStarted is when a goblin started: its worktree is made fresh by cfo
// spawn and kept across a switch, which writes a new spawn generation, so
// the generation's time dates the session only when the folder cannot.
func sessionStarted(meta state.TaskMeta) time.Time {
	if created := fileCreated(meta.Worktree); !created.IsZero() {
		return created
	}
	return spawnTime(meta.SpawnGen)
}

// briefWritten is when data/<id>/brief.md was written, which is when its
// task was queued, or zero without one.
func briefWritten(h home.Home, id string) time.Time {
	return fileCreated(filepath.Join(h.Data, id, "brief.md"))
}

// briefProject is the checkout a brief's Project section names, without a
// trailing parenthetical note, or empty when it names none.
func briefProject(path string) string {
	lines, err := fsx.ReadLines(path)
	if err != nil {
		return ""
	}
	for i, line := range lines {
		if strings.TrimSpace(line) != "## Project" {
			continue
		}
		for _, next := range lines[i+1:] {
			if next = strings.TrimSpace(next); next != "" {
				if strings.HasPrefix(next, "#") {
					return ""
				}
				checkout, _, _ := strings.Cut(next, " (")
				return strings.TrimSpace(checkout)
			}
		}
	}
	return ""
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
