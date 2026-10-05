package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// The fleet wakes are the ones the supervisor raises about the machine and
// the forge rather than about one goblin's screen: memory come back while
// work waits for it, and CI that finished on a goblin's pull request or went
// red on main, and pull requests that conflict or fall behind. They go
// through the wake queue like every other wake, so the CFO hears them
// however it is woken and cfo drain shows them.
const (
	fleetWakesSchema = "cfo-fleet-wakes.v1"
	// fleetWatchEvery is how often memory is read for memory_ready, the
	// cadence of the memory meter's own readings.
	fleetWatchEvery = time.Minute
	// ciPollEvery is how often GitHub is asked about CI. Every call counts
	// against the 5,000 an hour every goblin, gate and the CFO share (they
	// ran out on 2026-09-23). A poll lists PRs and main's runs once each,
	// compares open PRs in one batch, and reads details for each red run.
	ciPollEvery = 2 * time.Minute
	// ciPollSlack is how far short of ciPollEvery a reading may land and
	// still poll, since the readings come off a ticker whose jitter would
	// otherwise push every other poll a whole reading later.
	ciPollSlack = fleetWatchEvery / 2
	// memoryWakeGap is the least time between two memory wakes. ciWakeGap is
	// the least time between two wakes for one pull request, and between two
	// for one workflow on one repository's default branch, so a push that
	// turns two workflows red raises two wakes. A wake its gap holds waits
	// for the gap; it is never dropped.
	memoryWakeGap = 15 * time.Minute
	ciWakeGap     = 5 * time.Minute
	// repoWatchFor is how long a repository stays watched for its main's push
	// CI after the last goblin in it is gone, since a merge's CI usually ends
	// after the goblin that made it is retired.
	repoWatchFor = 6 * time.Hour
	// ciRecordFor is how long a pull request's reported checks are kept
	// after a live goblin last owned it open, and how long a key's last wake
	// is kept.
	ciRecordFor = 7 * 24 * time.Hour
	// ghCallTimeout bounds one gh or git call.
	ghCallTimeout = 30 * time.Second
)

// fleetWakes is what the fleet wakes remember between readings, in
// state/fleet-wakes.json, so a restart neither repeats a wake nor loses one.
type fleetWakes struct {
	Schema    string                  `json:"schema"`
	Progress  map[string]WorkProgress `json:"progress,omitempty"`
	Durations []CIDuration            `json:"durations,omitempty"`
	// MemoryAbove counts readings in a row with memory and commit both at or
	// above the next-start mark. MemorySpent says memory_ready woke since a
	// reading last fell under the floor.
	MemoryAbove  int       `json:"memory_above,omitempty"`
	MemorySpent  bool      `json:"memory_spent,omitempty"`
	MemoryReadAt time.Time `json:"memory_read_at,omitzero"`
	// Checks holds each goblin pull request's finished checks the CFO was
	// woken for, so each completion wakes once.
	Checks          map[string]reportedChecks   `json:"checks,omitempty"`
	Health          map[string]reportedPRHealth `json:"health,omitempty"`
	BackOff         map[string]time.Time        `json:"backoff,omitempty"`
	AllowanceFloors map[string]allowanceFloor   `json:"allowance_floors,omitempty"`
	// RedRuns holds, by repository, the red push runs of its main the CFO
	// was woken for.
	RedRuns map[string][]int64 `json:"red_runs,omitempty"`
	// Unreadable holds, by repository, why its CI could not be read on its
	// last poll.
	Unreadable map[string]unreadableRepo `json:"unreadable,omitempty"`
	// PRUnread holds, by repository, why the health of its listed pull
	// requests was not all read on its last poll.
	PRUnread       map[string]unreadPRs     `json:"pr_unread,omitempty"`
	OverlapPolled  map[string]time.Time     `json:"overlap_polled,omitempty"`
	OverlapUnread  map[string]string        `json:"overlap_unread,omitempty"`
	OverlapNotices map[string]overlapNotice `json:"overlap_notices,omitempty"`
	// Repos holds each repository watched and when a live goblin was last
	// seen working in it.
	Repos    map[string]time.Time `json:"repos,omitempty"`
	CIPolled time.Time            `json:"ci_polled,omitzero"`
	// Woke holds when each key last woke the CFO.
	Woke map[string]time.Time `json:"woke,omitempty"`
}

// reportedChecks is a pull request's checks as last reported: its head and
// each check's conclusion, and when a live goblin last owned it open.
type reportedChecks struct {
	Signature  string    `json:"signature"`
	At         time.Time `json:"at"`
	ReportedAt time.Time `json:"reported_at,omitzero"`
}

// unreadableRepo is the failure a repository's last poll met, and whether
// the CFO was woken for it.
type unreadableRepo struct {
	Failure string `json:"failure"`
	Woke    bool   `json:"woke,omitempty"`
}

func fleetWakesPath(stateDir string) string {
	return filepath.Join(stateDir, "fleet-wakes.json")
}

// readFleetWakes reads what the fleet wakes remember. A missing record is a
// fresh start; one that cannot be read is also started afresh, and the error
// says so, since a lost record can repeat a wake it already raised.
func readFleetWakes(stateDir string) (fleetWakes, error) {
	fresh := fleetWakes{Schema: fleetWakesSchema}
	data, err := fsx.ReadFile(fleetWakesPath(stateDir))
	if errors.Is(err, os.ErrNotExist) {
		return fresh, nil
	}
	if err != nil {
		return fresh, fmt.Errorf("fleet wakes: %w", err)
	}
	var w fleetWakes
	if err := json.Unmarshal(data, &w); err != nil || w.Schema != fleetWakesSchema {
		return fresh, fmt.Errorf("fleet wakes: %s is unreadable and starts afresh", fleetWakesPath(stateDir))
	}
	return w, nil
}

func writeFleetWakes(stateDir string, w fleetWakes) error {
	w.Schema = fleetWakesSchema
	data, err := json.Marshal(w)
	if err != nil {
		return err
	}
	return fsx.AtomicWriteFile(fleetWakesPath(stateDir), data)
}

// due reports whether key may wake the CFO again at now.
func (w *fleetWakes) due(key string, gap time.Duration, now time.Time) bool {
	last, ok := w.Woke[key]
	return !ok || now.Sub(last) >= gap
}

func (w *fleetWakes) woke(key string, now time.Time) {
	if w.Woke == nil {
		w.Woke = map[string]time.Time{}
	}
	w.Woke[key] = now
}

// keepFleetWakes reads memory every fleetWatchEvery and CI every ciPollEvery
// until ctx ends. It runs apart from the supervisor's loop, since a GitHub
// call can take seconds, and each reading hands what went wrong to the
// recovery cycle itself.
func (s *Service) keepFleetWakes(ctx context.Context, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		_ = s.checkFleet(ctx, time.Now().UTC())
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// checkFleet takes one memory reading and, when it is due, one CI poll. A
// supervisor that reads neither writes nothing. It returns what went wrong
// and hands it to the recovery cycle, which shows it on the board: what this
// reading met once, however many readings met it, and each repository whose
// CI cannot be read at every cycle until a poll reads it again.
func (s *Service) checkFleet(ctx context.Context, now time.Time) error {
	if (s.Options.Dispatch == nil || s.Options.Dispatch.Memory == nil) && s.Options.CI == nil && s.Options.Progress == nil {
		return nil
	}
	readingStarted := time.Now()
	currentTime := func() time.Time { return now.Add(time.Since(readingStarted)) }
	stateDir := s.Store.Home.State
	w, readErr := readFleetWakes(stateDir)
	err := errors.Join(readErr, s.pauseAtAllowanceFloor(ctx, &w, now), s.pollCI(ctx, &w, now, currentTime))
	err = errors.Join(err, s.checkMemory(ctx, &w, now), s.checkProgress(ctx, &w, now), writeFleetWakes(stateDir, w))
	var unreadable error
	if s.Options.CI != nil {
		repos := make([]string, 0, len(w.Unreadable))
		for repo := range w.Unreadable {
			repos = append(repos, repo)
		}
		sort.Strings(repos)
		for _, repo := range repos {
			unreadable = errors.Join(unreadable, errors.New(w.Unreadable[repo].Failure))
		}
		for _, repo := range slices.Sorted(maps.Keys(w.PRUnread)) {
			unreadable = errors.Join(unreadable, errors.New(w.PRUnread[repo].Failure))
		}
		for _, repo := range slices.Sorted(maps.Keys(w.OverlapUnread)) {
			unreadable = errors.Join(unreadable, errors.New(w.OverlapUnread[repo]))
		}
	}
	s.mu.Lock()
	if err != nil && (s.fleetErr == nil || !strings.Contains(s.fleetErr.Error(), err.Error())) {
		s.fleetErr = errors.Join(s.fleetErr, err)
	}
	s.ciUnreadable = unreadable
	s.mu.Unlock()
	return errors.Join(err, unreadable)
}

// checkMemory raises memory_ready once memory and commit both read at or
// above the next-start mark on two readings in a row while work waits for
// memory: a queued task a Start could start now, or a live goblin whose latest
// report is a wait on memory. It wakes once per crossing: not again until a
// reading falls under the floor and crosses back, and never twice within
// memoryWakeGap.
func (s *Service) checkMemory(ctx context.Context, w *fleetWakes, now time.Time) error {
	dispatch := s.Options.Dispatch
	if dispatch == nil || dispatch.Memory == nil {
		return nil
	}
	memory, err := dispatch.Memory()
	if err != nil {
		// No reading is no evidence either way: the next two decide.
		w.MemoryAbove = 0
		return nil
	}
	if w.MemoryReadAt.IsZero() || now.Sub(w.MemoryReadAt) > 2*fleetWatchEvery || !now.After(w.MemoryReadAt) {
		w.MemoryAbove = 0
	}
	w.MemoryReadAt = now
	low := min(memory.Available, memory.CommitAvailable)
	if low < memoryFloor {
		w.MemorySpent = false
	}
	if low < memoryNext {
		w.MemoryAbove = 0
		return nil
	}
	w.MemoryAbove++
	if err := s.schedule(ctx, now, memory, w); err != nil {
		return err
	}
	if w.MemoryAbove < 2 || w.MemorySpent || !w.due("memory", memoryWakeGap, now) {
		return nil
	}
	queued, waiting := memoryWork(s.Store.Home)
	if len(queued) == 0 && len(waiting) == 0 {
		return nil
	}
	if err := raiseFleetWake(s.Store.Home.State, "memory", "memory", memoryReadyDetail(memory, queued, waiting)); err != nil {
		return err
	}
	w.MemorySpent = true
	w.woke("memory", now)
	return nil
}

// memoryWork is the work waiting on memory: the queued tasks a Start could
// start now, top of the queue first, and the live goblins whose latest report
// is a wait on memory.
func memoryWork(h home.Home) (queued, waiting []string) {
	var candidates []string
	if backlog, err := fleet.ReadBacklog(h); err == nil {
		for _, row := range backlog.Queued {
			if row.Structured && state.ValidTaskID(row.ID) == nil {
				candidates = append(candidates, row.ID)
			}
		}
	}
	for _, task := range queuedBriefs(h, diskBriefs) {
		if !slices.Contains(candidates, task.ID) {
			candidates = append(candidates, task.ID)
		}
	}
	for _, id := range candidates {
		if _, err := planStart(h, id); err == nil {
			queued = append(queued, id)
		}
	}
	for _, meta := range liveTasks(h.State) {
		if record, err := state.ReadLifecycle(h.State, meta.ID); err == nil && record.Generation == meta.SpawnGen && record.Phase == "paused" && record.Pause != nil && record.Pause.Reason == "memory" {
			waiting = append(waiting, meta.ID)
			continue
		}
		lines, _ := state.TailStatus(h.State, meta.ID, 200)
		if _, report := latestReport(lines, spawnTime(meta.SpawnGen)); strings.HasPrefix(report, "waiting on memory: ") {
			waiting = append(waiting, meta.ID)
		}
	}
	return queued, waiting
}

// liveTasks reads every live task record in stateDir, in name order.
func liveTasks(stateDir string) []state.TaskMeta {
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		return nil
	}
	var tasks []state.TaskMeta
	for _, entry := range entries {
		id, isMeta := strings.CutSuffix(entry.Name(), ".meta")
		if entry.IsDir() || !isMeta || state.ValidTaskID(id) != nil {
			continue
		}
		if meta, err := state.ReadTaskMeta(stateDir, id); err == nil {
			tasks = append(tasks, meta)
		}
	}
	return tasks
}

// memoryReadyDetail is the memory_ready wake, which says what to do next.
func memoryReadyDetail(memory Memory, queued, waiting []string) string {
	var next []string
	if len(queued) > 0 {
		step := "dispatch the next queued task, " + queued[0]
		if len(queued) > 1 {
			step += " (also startable: " + strings.Join(queued[1:min(len(queued), 4)], ", ") + ")"
		}
		next = append(next, step)
	}
	if len(waiting) > 0 {
		next = append(next, "tell "+strings.Join(waiting, ", ")+" that the memory its heavy work waits on is free")
	}
	return fmt.Sprintf("memory_ready: %.1f GB of memory and %.1f GB of commit are free, both at or above the 5 GB mark on two readings in a row; next: %s",
		gigabytes(memory.Available), gigabytes(memory.CommitAvailable), strings.Join(next, ", and "))
}

// gigabytes rounds bytes down to a tenth of a gigabyte, so memory just under
// a mark never reads as the mark itself.
func gigabytes(bytes uint64) float64 {
	return math.Floor(float64(bytes)/(1<<30)*10) / 10
}

// raiseFleetWake appends one wake and publishes its episode, the way every
// other wake reaches the CFO.
func raiseFleetWake(stateDir, kind, key, detail string) error {
	if _, err := wake.Append(stateDir, kind, key, detail); err != nil {
		return err
	}
	_, err := wake.PublishEpisode(stateDir)
	return err
}

// ciGoblin is a live goblin whose pull requests' CI is watched: its
// repository, the branch its worktree has checked out, and the pull requests
// it recorded or reported.
type ciGoblin struct {
	id, repo, branch string
	pullRequests     []string
	meta             state.TaskMeta
}

// pollCI asks GitHub, every ciPollEvery, about the open pull requests of the
// repositories live goblins work in and about their main's push CI, and
// raises ci_finished for each goblin pull request whose checks have all
// concluded since it was last reported, for each red push run of main, and
// pr_health for each conflicting or behind pull request head.
// A repository with no origin remote is a local one and is asked nothing.
// One with an origin whose CI cannot be read is remembered in w.Unreadable
// until a poll reads it again, and raises ci_unreadable; one whose pull
// request health is not all read is remembered in w.PRUnread and raises
// pr_unread. A poll the supervisor's stop cuts short leaves both as they
// were.
func (s *Service) pollCI(ctx context.Context, w *fleetWakes, now time.Time, currentTime func() time.Time) error {
	runner := s.Options.CI
	if runner == nil || now.Sub(w.CIPolled) < ciPollEvery-ciPollSlack {
		return nil
	}
	w.CIPolled = now
	goblins := ciGoblins(ctx, runner, s.Store.Home.State)
	errs := s.pollAwaitedRuns(ctx, w, now)
	for _, repo := range w.watch(goblins, s.Store.Home.Root, now) {
		if currentTime().Before(w.BackOff[repo]) {
			continue
		}
		delete(w.BackOff, repo)
		probe, cancel := context.WithTimeout(ctx, ghCallTimeout)
		origin, err := runner.Run(probe, execx.Request{Dir: repo, Name: "git", Args: []string{"config", "--get", "remote.origin.url"}})
		cancel()
		if ctx.Err() != nil {
			return errs
		}
		if err != nil {
			errs = errors.Join(errs, fmt.Errorf("ci wakes: read the origin of %s: %w", repo, err))
			continue
		}
		if origin.ExitCode != 0 {
			delete(w.Unreadable, repo)
			delete(w.PRUnread, repo)
			delete(w.OverlapUnread, repo)
			continue
		}
		var mine []ciGoblin
		for _, goblin := range goblins {
			if fsx.SamePath(goblin.repo, repo) {
				mine = append(mine, goblin)
			}
		}
		pollRunner := githubPollRunner{commands: runner, state: w, repo: repo, now: currentTime}
		pullsUnreadable, pullsErr := pollPullRequests(ctx, pollRunner, s.Store.Home.State, w, repo, mine, now)
		mainUnreadable, mainErr := pollMain(ctx, pollRunner, s.Store.Home.State, w, repo, now)
		overlapErr := s.pollOverlaps(ctx, pollRunner, w, repo, mine, currentTime)
		if ctx.Err() != nil {
			return errors.Join(errs, pullsErr, mainErr)
		}
		errs = errors.Join(errs, pullsErr, mainErr, overlapErr, reportUnreadable(s.Store.Home.State, w, repo, errors.Join(pullsUnreadable, mainUnreadable), now))
	}
	for url, checks := range w.Checks {
		if now.Sub(checks.At) >= ciRecordFor {
			delete(w.Checks, url)
		}
	}
	for url, health := range w.Health {
		if now.Sub(health.At) >= ciRecordFor {
			delete(w.Health, url)
		}
	}
	for key, last := range w.Woke {
		if now.Sub(last) >= ciRecordFor {
			delete(w.Woke, key)
		}
	}
	return errs
}

// reportUnreadable remembers why repo's CI could not be read and raises
// ci_unreadable once the same failure was met on two polls in a row: once
// for as long as it stays the same, and again for a different failure or
// for one that cleared and came back. A repository that reads again is
// forgotten and wakes nobody.
func reportUnreadable(stateDir string, w *fleetWakes, repo string, failure error, now time.Time) error {
	if failure == nil {
		delete(w.Unreadable, repo)
		return nil
	}
	if w.Unreadable == nil {
		w.Unreadable = map[string]unreadableRepo{}
	}
	record, key := w.Unreadable[repo], "ci:repo:"+repo
	if record.Failure != failure.Error() {
		w.Unreadable[repo] = unreadableRepo{Failure: failure.Error()}
		return nil
	}
	if record.Woke || !w.due(key, ciWakeGap, now) {
		return nil
	}
	detail := fmt.Sprintf("ci_unreadable: %s; next: sign gh in with gh auth login, or point origin at GitHub, or set origin's default branch with git remote set-head origin -a; no CI wake comes from this repository until it reads again", record.Failure)
	if err := raiseFleetWake(stateDir, "ci", "repo:"+filepath.Base(repo), detail); err != nil {
		return err
	}
	record.Woke = true
	w.Unreadable[repo] = record
	w.woke(key, now)
	return nil
}

// ciGoblins reads every live goblin's repository, branch and pull requests.
func ciGoblins(ctx context.Context, runner execx.Runner, stateDir string) []ciGoblin {
	var goblins []ciGoblin
	for _, meta := range liveTasks(stateDir) {
		if meta.Project == "" {
			continue
		}
		goblin := ciGoblin{id: meta.ID, repo: filepath.Clean(meta.Project), meta: meta}
		if record, err := state.ReadLifecycle(stateDir, meta.ID); err == nil && record.Generation == meta.SpawnGen && record.Pause != nil && (record.Pause.Reason == "ci" || record.Pause.Reason == "deploy") {
			wait, _, _ := strings.Cut(record.Pause.Until, "@")
			if target, isPR := strings.CutPrefix(wait, "pr:"); isPR {
				goblin.pullRequests = append(goblin.pullRequests, target)
			}
		}
		if meta.Worktree != "" {
			goblin.branch, _ = runOutput(ctx, runner, meta.Worktree, "git", "branch", "--show-current")
		}
		if kv, err := state.ReadMeta(filepath.Join(stateDir, meta.ID+".meta")); err == nil && kv["pr"] != "" {
			goblin.pullRequests = append(goblin.pullRequests, kv["pr"])
		}
		lines, _ := state.TailStatus(stateDir, meta.ID, 200)
		spawned := spawnTime(meta.SpawnGen)
		for _, line := range lines {
			stamp, event := state.SplitStatus(line)
			if !spawned.IsZero() && stamp.Before(spawned.Truncate(time.Second)) {
				continue
			}
			if rest, ok := strings.CutPrefix(strings.TrimSpace(event), "done: PR "); ok {
				if fields := strings.Fields(rest); len(fields) > 0 && githubPullRequest.MatchString(fields[0]) {
					goblin.pullRequests = append(goblin.pullRequests, fields[0])
				}
			}
		}
		goblins = append(goblins, goblin)
	}
	return goblins
}

// watch records the repositories live goblins work in, forgets those no
// goblin has worked in for repoWatchFor, and returns the ones to poll: those
// and this home, when it is a checkout, in name order.
func (w *fleetWakes) watch(goblins []ciGoblin, homeRoot string, now time.Time) []string {
	if w.Repos == nil {
		w.Repos = map[string]time.Time{}
	}
	seen := func(repo string) {
		for known := range w.Repos {
			if fsx.SamePath(known, repo) {
				w.Repos[known] = now
				return
			}
		}
		w.Repos[repo] = now
	}
	for _, goblin := range goblins {
		seen(goblin.repo)
	}
	if homeRoot != "" && exists(filepath.Join(homeRoot, ".git")) {
		seen(filepath.Clean(homeRoot))
	}
	repos := make([]string, 0, len(w.Repos))
	for repo, last := range w.Repos {
		if now.Sub(last) >= repoWatchFor {
			delete(w.Repos, repo)
			delete(w.RedRuns, repo)
			delete(w.Unreadable, repo)
			delete(w.PRUnread, repo)
			delete(w.OverlapPolled, repo)
			delete(w.OverlapUnread, repo)
			delete(w.BackOff, repo)
			continue
		}
		repos = append(repos, repo)
	}
	sort.Strings(repos)
	return repos
}

// ghPullRequest is one open pull request as gh pr list reports it.
type ghPullRequest struct {
	Number            int       `json:"number"`
	URL               string    `json:"url"`
	HeadRefName       string    `json:"headRefName"`
	HeadRefOid        string    `json:"headRefOid"`
	Checks            []ghCheck `json:"statusCheckRollup"`
	Mergeable         string    `json:"mergeable"`
	BaseRefName       string    `json:"baseRefName"`
	IsCrossRepository bool      `json:"isCrossRepository"`
	Author            struct {
		Login string `json:"login"`
	} `json:"author"`
}

// ghCheck is one entry of a pull request's check rollup: a check run, with a
// status and a conclusion, or a commit status context, with a state.
type ghCheck struct {
	Kind        string `json:"__typename"`
	Name        string `json:"name"`
	Context     string `json:"context"`
	Status      string `json:"status"`
	Conclusion  string `json:"conclusion"`
	State       string `json:"state"`
	CompletedAt string `json:"completedAt"`
	StartedAt   string `json:"startedAt"`
}

func (c ghCheck) name() string {
	if c.Kind == "StatusContext" {
		return c.Context
	}
	return c.Name
}

// concluded reports whether the check has finished.
func (c ghCheck) concluded() bool {
	if c.Kind == "StatusContext" {
		return c.State != "" && c.State != "PENDING" && c.State != "EXPECTED"
	}
	return c.Status == "COMPLETED"
}

// passed reports whether a finished check passed: a success, or a check that
// was skipped or neutral, as GitHub's own merge box counts them.
func (c ghCheck) passed() bool {
	if c.Kind == "StatusContext" {
		return c.State == "SUCCESS"
	}
	switch c.Conclusion {
	case "SUCCESS", "NEUTRAL", "SKIPPED":
		return true
	}
	return false
}

func (c ghCheck) outcome() string {
	if c.Kind == "StatusContext" {
		return c.State
	}
	return c.Conclusion
}

// pollPullRequests lists repo's open pull requests once and raises
// ci_finished for each one of goblins whose checks have all concluded with
// a result it was not woken for: its head and each check's conclusion, and
// pr_health or pr_unread for every open pull request. It returns why the
// pull requests could not be listed, apart from what went wrong raising a
// wake; health left unread stays in w.PRUnread.
func pollPullRequests(ctx context.Context, runner execx.Runner, stateDir string, w *fleetWakes, repo string, goblins []ciGoblin, now time.Time) (unreadable, err error) {
	out, err := runOutput(ctx, runner, repo, "gh", "pr", "list", "--state", "open", "--limit", "100", "--json", "number,url,headRefName,headRefOid,statusCheckRollup,mergeable,baseRefName,isCrossRepository,author")
	if err != nil {
		return fmt.Errorf("ci wakes: list the open pull requests of %s: %w", repo, err), nil
	}
	var open []ghPullRequest
	if err := json.Unmarshal([]byte(out), &open); err != nil {
		return fmt.Errorf("ci wakes: gh listed the open pull requests of %s in a shape it cannot read: %w", repo, err), nil
	}
	if open == nil {
		return fmt.Errorf("ci wakes: the open pull requests of %s were not read", repo), nil
	}
	for _, pr := range open {
		if pr.Number <= 0 || pr.HeadRefOid == "" || !githubPullRequest.MatchString(pr.URL) {
			return fmt.Errorf("ci wakes: an invalid pull request was listed for %s", repo), nil
		}
	}
	var errs error
	for _, goblin := range goblins {
		for _, pr := range open {
			if !slices.Contains(goblin.pullRequests, pr.URL) && (pr.IsCrossRepository || goblin.branch == "" || pr.HeadRefName != goblin.branch) {
				continue
			}
			if reported, ok := w.Checks[pr.URL]; ok {
				reported.At = now
				w.Checks[pr.URL] = reported
			}
			errs = errors.Join(errs, reportChecks(stateDir, w, goblin.id, pr, now))
		}
	}
	comparisons, unreadComparisons, branch, unread := comparePullRequests(ctx, runner, repo, open)
	if ctx.Err() != nil {
		return nil, errs
	}
	isCapped := len(open) >= 100
	if isCapped {
		unread = errors.Join(unread, fmt.Errorf("PR health: %s listed the first 100 open pull requests; any further pull requests were not read", repo))
	}
	if w.Health == nil {
		w.Health = map[string]reportedPRHealth{}
	}
	var unreadHeads []ghPullRequest
	for _, pr := range open {
		record := w.Health[pr.URL]
		if record.Head != pr.HeadRefOid {
			record = reportedPRHealth{Head: pr.HeadRefOid}
		}
		record.At = now
		w.Health[pr.URL] = record
		if !record.HasUnreadWake && slices.ContainsFunc(unreadComparisons, func(unread ghPullRequest) bool { return unread.URL == pr.URL }) {
			unreadHeads = append(unreadHeads, pr)
		}
		comparison := comparisons[pr.Number]
		if comparison == nil && pr.Mergeable != "CONFLICTING" {
			continue
		}
		var owner string
		for _, goblin := range goblins {
			if slices.Contains(goblin.pullRequests, pr.URL) || !pr.IsCrossRepository && goblin.branch != "" && pr.HeadRefName == goblin.branch {
				owner = goblin.id
				break
			}
		}
		behind := 0
		if comparison != nil {
			behind = *comparison.BehindBy
		}
		errs = errors.Join(errs, reportPRHealth(stateDir, w, owner, pr, branch, behind, now))
	}
	return nil, errors.Join(errs, reportPRUnread(stateDir, w, repo, unreadHeads, isCapped, unread, now))
}

// reportChecks raises ci_finished for pr once every check on it has
// concluded, unless the CFO was already woken for this very result.
func reportChecks(stateDir string, w *fleetWakes, goblin string, pr ghPullRequest, now time.Time) error {
	if len(pr.Checks) == 0 {
		return nil
	}
	var parts, failed []string
	for _, check := range pr.Checks {
		if !check.concluded() {
			return nil
		}
		parts = append(parts, check.name()+"="+check.outcome()+"@"+check.CompletedAt)
		if !check.passed() {
			failed = append(failed, check.name())
		}
	}
	sort.Strings(parts)
	signature := pr.HeadRefOid + "|" + strings.Join(parts, "|")
	key := "ci:" + pr.URL
	if completed := w.Checks[pr.URL]; completed.Signature == signature {
		completed.ReportedAt = now
		w.Checks[pr.URL] = completed
		return nil
	}
	if !w.due(key, ciWakeGap, now) {
		return nil
	}
	head := pr.HeadRefOid
	if len(head) > 7 {
		head = head[:7]
	}
	detail := fmt.Sprintf("ci_finished: %s's PR #%d (%s) finished its checks at %s: ", goblin, pr.Number, pr.HeadRefName, head)
	if len(failed) == 0 {
		detail += fmt.Sprintf("all %d passed; next: check the base and the merge ref's first parent, then merge it with a merge commit if its work is done (%s)", len(pr.Checks), pr.URL)
	} else {
		sort.Strings(failed)
		detail += fmt.Sprintf("%d of %d failed (%s); next: tell %s which checks failed with cfo send %s \"...\" so it fixes them (%s)", len(failed), len(pr.Checks), strings.Join(failed, ", "), goblin, goblin, pr.URL)
	}
	if err := raiseFleetWake(stateDir, "ci", goblin, detail); err != nil {
		return err
	}
	if w.Checks == nil {
		w.Checks = map[string]reportedChecks{}
	}
	w.Checks[pr.URL] = reportedChecks{Signature: signature, At: now, ReportedAt: now}
	for _, check := range pr.Checks {
		started, _ := time.Parse(time.RFC3339, check.StartedAt)
		finished, _ := time.Parse(time.RFC3339, check.CompletedAt)
		kind := "ci"
		if strings.Contains(strings.ToLower(check.name()), "deploy") {
			kind = "deploy"
		}
		recordCIDuration(w, pr.URL, kind, check.name(), started, finished)
	}
	w.woke(key, now)
	return nil
}

// ghRun is one workflow run as gh run list reports it.
type ghRun struct {
	ID         int64     `json:"databaseId"`
	Workflow   string    `json:"workflowName"`
	Status     string    `json:"status"`
	Conclusion string    `json:"conclusion"`
	HeadSHA    string    `json:"headSha"`
	URL        string    `json:"url"`
	Attempt    int       `json:"attempt"`
	StartedAt  time.Time `json:"startedAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// red reports whether a finished run failed. A cancelled run was superseded
// or stopped by hand, which is not main breaking.
func (r ghRun) red() bool {
	switch r.Conclusion {
	case "failure", "timed_out", "startup_failure":
		return r.Status == "completed"
	}
	return false
}

// defaultBranch names repo's default branch from its own refs, asking GitHub
// nothing: the branch origin's HEAD names, else main, else master, whichever
// origin has. A repository with none of them is an error, never taken for
// main, since its red runs would then go unseen in silence.
func defaultBranch(ctx context.Context, runner execx.Runner, repo string) (string, error) {
	if head, err := runOutput(ctx, runner, repo, "git", "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		if name, ok := strings.CutPrefix(head, "origin/"); ok && name != "" {
			return name, nil
		}
	}
	for _, name := range []string{"main", "master"} {
		if _, err := runOutput(ctx, runner, repo, "git", "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+name); err == nil {
			return name, nil
		}
	}
	return "", fmt.Errorf("ci wakes: the default branch of %s cannot be found: its origin has no HEAD, no main and no master", repo)
}

// pollMain raises ci_finished for each workflow whose newest push run on
// repo's default branch finished red and was not reported yet, naming the
// workflow, the jobs that failed and the run. On 2026-09-30 main's install
// workflow stayed red for hours before the CFO noticed. It returns why the
// runs could not be read, apart from what went wrong raising a wake.
func pollMain(ctx context.Context, runner execx.Runner, stateDir string, w *fleetWakes, repo string, now time.Time) (unreadable, err error) {
	branch, err := defaultBranch(ctx, runner, repo)
	if err != nil {
		return err, nil
	}
	out, err := runOutput(ctx, runner, repo, "gh", "run", "list", "--branch", branch, "--event", "push", "--limit", "20", "--json", "databaseId,workflowName,status,conclusion,headSha,url,startedAt,updatedAt")
	if err != nil {
		return fmt.Errorf("ci wakes: list the push runs of %s's %s: %w", repo, branch, err), nil
	}
	var runs []ghRun
	if err := json.Unmarshal([]byte(out), &runs); err != nil {
		return fmt.Errorf("ci wakes: gh listed the push runs of %s in a shape it cannot read: %w", repo, err), nil
	}
	newest := map[string]bool{}
	var errs error
	for _, run := range runs {
		if newest[run.Workflow] {
			continue
		}
		newest[run.Workflow] = true
		key := "ci:main:" + repo + ":" + run.Workflow
		if !run.red() || slices.Contains(w.RedRuns[repo], run.ID) || !w.due(key, ciWakeGap, now) {
			continue
		}
		jobs, jobsErr := failedJobs(ctx, runner, repo, run.ID)
		unreadable = errors.Join(unreadable, jobsErr)
		head := run.HeadSHA
		if len(head) > 7 {
			head = head[:7]
		}
		detail := fmt.Sprintf("ci_finished: %s's push CI is red in %s: workflow %s, %s, run %d at %s (%s); next: read it with gh run view %d --log-failed and dispatch a fix",
			branch, filepath.Base(repo), run.Workflow, jobs, run.ID, head, run.URL, run.ID)
		if err := raiseFleetWake(stateDir, "ci", "main:"+filepath.Base(repo), detail); err != nil {
			errs = errors.Join(errs, err)
			continue
		}
		if w.RedRuns == nil {
			w.RedRuns = map[string][]int64{}
		}
		kind := "ci"
		if strings.Contains(strings.ToLower(run.Workflow), "deploy") {
			kind = "deploy"
		}
		recordCIDuration(w, run.URL, kind, run.Workflow, run.StartedAt, run.UpdatedAt)
		reported := append(w.RedRuns[repo], run.ID)
		w.RedRuns[repo] = reported[max(0, len(reported)-20):]
		w.woke(key, now)
	}
	return unreadable, errs
}

// failedJobs names the jobs of run that failed, as the wake reads them.
func failedJobs(ctx context.Context, runner execx.Runner, repo string, run int64) (string, error) {
	out, err := runOutput(ctx, runner, repo, "gh", "run", "view", fmt.Sprint(run), "--json", "jobs")
	var view struct {
		Jobs []struct {
			Name       string `json:"name"`
			Conclusion string `json:"conclusion"`
		} `json:"jobs"`
	}
	if err != nil {
		return "its failing jobs unread", fmt.Errorf("ci wakes: read the failing jobs of %s's run %d: %w", repo, run, err)
	}
	if err := json.Unmarshal([]byte(out), &view); err != nil {
		return "its failing jobs unread", fmt.Errorf("ci wakes: cannot read the failing jobs of %s's run %d: %w", repo, run, err)
	}
	var failed []string
	for _, job := range view.Jobs {
		if job.Conclusion == "failure" || job.Conclusion == "timed_out" {
			failed = append(failed, job.Name)
		}
	}
	switch len(failed) {
	case 0:
		return "no job marked failed", nil
	case 1:
		return "job " + failed[0], nil
	}
	return "jobs " + strings.Join(failed, ", "), nil
}

// runOutput runs name with args in dir, bounded by ghCallTimeout, and returns
// its trimmed output, or an error naming what it printed to stderr.
func runOutput(ctx context.Context, runner execx.Runner, dir, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, ghCallTimeout)
	defer cancel()
	result, err := runner.Run(ctx, execx.Request{Dir: dir, Name: name, Args: args})
	if err != nil {
		return "", err
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("%s %s exited %d: %s", name, args[0], result.ExitCode, bounded(strings.TrimSpace(string(result.Stderr)), 300))
	}
	return strings.TrimSpace(string(result.Stdout)), nil
}
