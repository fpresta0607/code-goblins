// Package ciparity keeps the parity record of a repository's desktop CI
// mirror: each job the desktop CI runner ran beside the hosted job it copies,
// on the same tree, so a hosted job retires only after its desktop copy
// matched it run after run.
//
// The mirror is a workflow of its own that a CI run's completion starts,
// titled "Desktop mirror of CI run <id>", whose job desktop-<name> copies the
// job <name> of that CI run. The hosted copy's log names the merge it checked
// out, "HEAD is now at <sha> Merge <head> into <base>", and the desktop's the
// merge it made again, "desktop mirror: Merge <head> into <base>".
package ciparity

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// MirrorPrefix starts the name of a desktop job: desktop-backend-shard (1)
// copies backend-shard (1).
const MirrorPrefix = "desktop-"

// ReadyAfter is how many pull request runs in a row a desktop job must
// match its hosted copy before the hosted job may retire: the same
// conclusion and the same pytest counts, on the same tree, with no desktop
// run lost.
const ReadyAfter = 20

var (
	// mirrorTitle names the CI run a mirror run copies.
	mirrorTitle = regexp.MustCompile(`^Desktop mirror of CI run ([0-9]+)$`)
	// hostedMerge is the merge the hosted copy's checkout made.
	hostedMerge = regexp.MustCompile(`HEAD is now at [0-9a-f]+ Merge ([0-9a-f]{40}) into ([0-9a-f]{40})`)
	// desktopMerge is the merge the desktop copy made again.
	desktopMerge = regexp.MustCompile(`desktop mirror: Merge ([0-9a-f]{40}) into ([0-9a-f]{40})`)
	// summary is pytest's last line, "===== 7446 passed, 2 skipped in 405.90s =====".
	summary = regexp.MustCompile(`=+ (.*?) in [0-9.]+s(?: \([0-9:]+\))? =+`)
	// countWord is one count of a summary, "7446 passed".
	countWord = regexp.MustCompile(`([0-9]+) (passed|failed|skipped|errors?|xfailed|xpassed|deselected)\b`)
	// runnerStarted names the runner a desktop job's VM started with, in the
	// log of the turn the watcher started for it.
	runnerStarted = regexp.MustCompile(`the VM started with runner (\S+)`)
	// tookTurn and heldBy are what a turn's log says of its wait.
	tookTurn = regexp.MustCompile(`took its turn after (\S+)`)
	heldBy   = regexp.MustCompile(`the turn is held by (.*?); `)
)

// Counts are what pytest's summary line counts. Warnings are left out: they
// say nothing of a result.
type Counts struct {
	Passed     int `json:"passed"`
	Failed     int `json:"failed"`
	Skipped    int `json:"skipped"`
	Errors     int `json:"errors"`
	XFailed    int `json:"xfailed"`
	XPassed    int `json:"xpassed"`
	Deselected int `json:"deselected"`
}

func (c Counts) String() string {
	return fmt.Sprintf("%d passed, %d failed, %d skipped, %d errors", c.Passed, c.Failed, c.Skipped, c.Errors)
}

// Copy is one copy of a job: its GitHub job, how it concluded, pytest's
// counts when its log has them, the merge it tested, how long it ran, and
// the runner it ran on.
type Copy struct {
	Job        int64   `json:"job"`
	Conclusion string  `json:"conclusion"`
	Counts     *Counts `json:"counts,omitempty"`
	Head       string  `json:"head,omitempty"`
	Base       string  `json:"base,omitempty"`
	Seconds    float64 `json:"seconds"`
	Runner     string  `json:"runner,omitempty"`
}

// Pair is a hosted job and its desktop copy for one CI run, with how long
// the desktop's turn waited in the machine's line and what held it last,
// when the watcher's log of that turn was found.
type Pair struct {
	CIRun     int64     `json:"ci_run"`
	MirrorRun int64     `json:"mirror_run"`
	Job       string    `json:"job"`
	Finished  time.Time `json:"finished"`
	Hosted    Copy      `json:"hosted"`
	Desktop   Copy      `json:"desktop"`
	Waited    string    `json:"waited,omitempty"`
	HeldBy    string    `json:"held_by,omitempty"`
}

// Differences says how the desktop copy differs from the hosted one, none
// when it matched: the same tree, the same conclusion, the same counts, and
// a desktop run that was not lost.
func (p Pair) Differences() []string {
	var differences []string
	switch p.Desktop.Conclusion {
	case "success", "failure":
	default:
		differences = append(differences, "the desktop run was lost, "+cmp.Or(p.Desktop.Conclusion, "with no conclusion"))
	}
	if p.Hosted.Head == "" || p.Hosted.Head != p.Desktop.Head || p.Hosted.Base != p.Desktop.Base {
		differences = append(differences, "the two copies did not show the same merge tested")
	}
	if p.Hosted.Conclusion != p.Desktop.Conclusion {
		differences = append(differences, fmt.Sprintf("hosted %s, desktop %s", p.Hosted.Conclusion, p.Desktop.Conclusion))
	}
	switch {
	case p.Hosted.Counts == nil || p.Desktop.Counts == nil:
		differences = append(differences, "a copy's log has no pytest summary")
	case *p.Hosted.Counts != *p.Desktop.Counts:
		differences = append(differences, fmt.Sprintf("hosted %s, desktop %s", p.Hosted.Counts, p.Desktop.Counts))
	}
	return differences
}

// ParseCounts reads pytest's last summary line in log.
func ParseCounts(log string) (Counts, bool) {
	found := summary.FindAllStringSubmatch(log, -1)
	if len(found) == 0 {
		return Counts{}, false
	}
	var counts Counts
	for _, part := range countWord.FindAllStringSubmatch(found[len(found)-1][1], -1) {
		number, _ := strconv.Atoi(part[1])
		switch part[2] {
		case "passed":
			counts.Passed = number
		case "failed":
			counts.Failed = number
		case "skipped":
			counts.Skipped = number
		case "error", "errors":
			counts.Errors = number
		case "xfailed":
			counts.XFailed = number
		case "xpassed":
			counts.XPassed = number
		case "deselected":
			counts.Deselected = number
		}
	}
	return counts, true
}

// Run is a workflow run as GitHub lists it.
type Run struct {
	ID         int64  `json:"id"`
	Title      string `json:"display_title"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}

// Job is a workflow run's job as GitHub lists it.
type Job struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	Conclusion string    `json:"conclusion"`
	Started    time.Time `json:"started_at"`
	Completed  time.Time `json:"completed_at"`
	Runner     string    `json:"runner_name"`
}

// GitHub reads what the record needs from the repository: the mirror
// workflow's newest finished runs, a run's jobs and a job's log.
type GitHub interface {
	MirrorRuns(ctx context.Context, limit int) ([]Run, error)
	Jobs(ctx context.Context, run int64) ([]Job, error)
	Log(ctx context.Context, job int64) (string, error)
}

// Collect reads the pairs of the mirror's newest limit finished runs that
// record does not hold yet. turnLogs is the folder of the logs of the turns
// the watcher started for desktop jobs, which say how long each waited.
func Collect(ctx context.Context, github GitHub, record Record, limit int, turnLogs string) ([]Pair, error) {
	runs, err := github.MirrorRuns(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("ciparity: list the mirror's runs: %w", err)
	}
	var pairs []Pair
	for _, run := range runs {
		found := mirrorTitle.FindStringSubmatch(run.Title)
		if found == nil || run.Status != "completed" {
			continue
		}
		ciRun, err := strconv.ParseInt(found[1], 10, 64)
		if err != nil {
			continue
		}
		mirrorJobs, err := github.Jobs(ctx, run.ID)
		if err != nil {
			return pairs, fmt.Errorf("ciparity: read the jobs of mirror run %d: %w", run.ID, err)
		}
		var hostedJobs []Job
		for _, desktop := range mirrorJobs {
			name, isMirrored := strings.CutPrefix(desktop.Name, MirrorPrefix)
			if !isMirrored || desktop.Conclusion == "skipped" || record.has(run.ID, name) {
				continue
			}
			if hostedJobs == nil {
				if hostedJobs, err = github.Jobs(ctx, ciRun); err != nil {
					return pairs, fmt.Errorf("ciparity: read the jobs of CI run %d: %w", ciRun, err)
				}
			}
			at := slices.IndexFunc(hostedJobs, func(job Job) bool { return job.Name == name })
			if at < 0 {
				continue
			}
			pair, hasPair, err := pairOf(ctx, github, ciRun, run.ID, name, hostedJobs[at], desktop)
			if err != nil {
				return pairs, err
			}
			if hasPair {
				pair.Waited, pair.HeldBy = turnWait(turnLogs, desktop.Runner)
				pairs = append(pairs, pair)
			}
		}
	}
	return pairs, nil
}

// pairOf reads both copies' logs, the desktop's first: a desktop job whose
// hosted copy did not run to a verdict mirrored nothing, which its log says,
// and is no pair.
func pairOf(ctx context.Context, github GitHub, ciRun, mirrorRun int64, name string, hosted, desktop Job) (Pair, bool, error) {
	pair := Pair{CIRun: ciRun, MirrorRun: mirrorRun, Job: name, Finished: hosted.Completed}
	for _, side := range []struct {
		job   Job
		copy  *Copy
		merge *regexp.Regexp
	}{{desktop, &pair.Desktop, desktopMerge}, {hosted, &pair.Hosted, hostedMerge}} {
		log, err := github.Log(ctx, side.job.ID)
		if err != nil {
			return Pair{}, false, fmt.Errorf("ciparity: read the log of job %d (%s): %w", side.job.ID, side.job.Name, err)
		}
		if side.copy == &pair.Desktop && strings.Contains(log, "there is nothing to mirror") {
			return Pair{}, false, nil
		}
		*side.copy = Copy{Job: side.job.ID, Conclusion: side.job.Conclusion, Seconds: side.job.Completed.Sub(side.job.Started).Seconds(), Runner: side.job.Runner}
		if counts, isFound := ParseCounts(log); isFound {
			side.copy.Counts = &counts
		}
		if found := side.merge.FindStringSubmatch(log); found != nil {
			side.copy.Head, side.copy.Base = found[1], found[2]
		}
	}
	return pair, true, nil
}

// turnWait reads, from the log of the turn the watcher started for the job
// that ran on runner, how long the turn waited and what held the line last.
// None is read when no such log is found, as for a job no turn ran.
func turnWait(folder, runner string) (waited, holder string) {
	if runner == "" {
		return "", ""
	}
	logs, _ := filepath.Glob(filepath.Join(folder, "job-*.log"))
	for _, path := range logs {
		data, err := fsx.ReadFile(path)
		if err != nil {
			continue
		}
		text := string(data)
		if started := runnerStarted.FindStringSubmatch(text); started == nil || started[1] != runner {
			continue
		}
		if took := tookTurn.FindStringSubmatch(text); took != nil {
			waited = took[1]
		}
		if held := heldBy.FindAllStringSubmatch(text, -1); len(held) > 0 {
			holder = held[len(held)-1][1]
		}
		return waited, holder
	}
	return "", ""
}

// Record is a repository's parity record, as the fleet keeps it.
type Record struct {
	Repository string `json:"repository"`
	Pairs      []Pair `json:"pairs"`
}

func (r Record) has(mirrorRun int64, job string) bool {
	return slices.ContainsFunc(r.Pairs, func(pair Pair) bool { return pair.MirrorRun == mirrorRun && pair.Job == job })
}

// Path is where the record of repository, as owner/name, is kept under the
// folder the watcher keeps the desktop runner's logs in.
func Path(folder, repository string) string {
	return filepath.Join(folder, "parity", strings.ReplaceAll(repository, "/", "-")+".json")
}

// Read reads the record at path, an empty one for repository when there is
// none yet.
func Read(path, repository string) (Record, error) {
	data, err := fsx.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Record{Repository: repository}, nil
	}
	if err != nil {
		return Record{}, err
	}
	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		return Record{}, fmt.Errorf("ciparity: the record %s cannot be read: %w", path, err)
	}
	if record.Repository != repository {
		return Record{}, fmt.Errorf("ciparity: the record %s is %s's, not %s's", path, record.Repository, repository)
	}
	return record, nil
}

// Write keeps record at path, with its pairs in the order their hosted jobs
// finished.
func Write(path string, record Record) error {
	slices.SortStableFunc(record.Pairs, func(a, b Pair) int { return a.Finished.Compare(b.Finished) })
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return fsx.AtomicWriteFile(path, append(data, '\n'))
}

// Standing is where one job stands: how many runs in a row its desktop copy
// matched, up to the newest, how many pairs there are, how many desktop
// runs were lost, and the newest difference, with its CI run.
type Standing struct {
	Job        string
	Streak     int
	Pairs      int
	Lost       int
	Difference string
	DifferedOn int64
}

// IsReady says whether the job's hosted copy may retire: ReadyAfter runs in
// a row matched.
func (s Standing) IsReady() bool {
	return s.Streak >= ReadyAfter
}

// Standings are each job's standing in the record, by job name.
func Standings(record Record) []Standing {
	pairs := slices.Clone(record.Pairs)
	slices.SortStableFunc(pairs, func(a, b Pair) int { return a.Finished.Compare(b.Finished) })
	byJob := map[string]*Standing{}
	var names []string
	for _, pair := range pairs {
		standing := byJob[pair.Job]
		if standing == nil {
			standing = &Standing{Job: pair.Job}
			byJob[pair.Job] = standing
			names = append(names, pair.Job)
		}
		standing.Pairs++
		differences := pair.Differences()
		if pair.Desktop.Conclusion != "success" && pair.Desktop.Conclusion != "failure" {
			standing.Lost++
		}
		if len(differences) > 0 {
			standing.Streak, standing.Difference, standing.DifferedOn = 0, strings.Join(differences, "; "), pair.CIRun
			continue
		}
		standing.Streak++
	}
	slices.Sort(names)
	standings := make([]Standing, 0, len(names))
	for _, name := range names {
		standings = append(standings, *byJob[name])
	}
	return standings
}
