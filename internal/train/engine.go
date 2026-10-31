package train

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/lock"
)

const (
	// noCIWithin is how long a pushed run waits for GitHub to show its head
	// and for a check to appear on it: a run whose push was lost is pushed
	// again, and a repository whose CI never starts stops its train rather
	// than holding it, and every pull request on it, for ever.
	noCIWithin = 20 * time.Minute
	// RunDeadline bounds a run whose checks never finish, as when the
	// repository's runners are down.
	RunDeadline = 3 * time.Hour
	// maxMoved is how many runs in a row the base may move during before the
	// train stops: on a main that never holds still nothing it tests lands.
	maxMoved = 3
	// maxErrors is how many steps in a row may fail before the train stops.
	// A failed step leaves the train as its record says, for the next step
	// to take on, since most failures are a network or a restart.
	maxErrors = 5
	// mergedReads bounds how often GitHub is read for the pull requests a run
	// landed to show as merged, and mergedReadAfter is the pause between.
	mergedReads     = 3
	mergedReadAfter = 5 * time.Second
)

var (
	// ErrRunning says a train is already running on the repository.
	ErrRunning = errors.New("a merge train is already running on this repository")
	// ErrBusy says another process is taking the train's next step.
	ErrBusy         = errors.New("another process is moving this merge train")
	errMergeRefused = errors.New("the tested pull request did not merge")
)

// Repository is where a train runs: the GitHub repository as owner/name, a
// local clone of it whose origin is that repository, and the branch pull
// requests merge into.
//
// LandsLoneRider lets a train of one pull request land it on its own CI run
// when that run tested the tree that would land (own_run.go). It holds only
// where a pull request's own run starts every job a train's run would: a
// repository whose workflows test less on a pull request's own run, as
// code-goblins' do, leaves it off.
//
// LoneRiderWait is how long one finished pull request waits for a second to
// share a train run with before it rides alone, which the supervisor reads.
type Repository struct {
	Slug           string
	Checkout       string
	Base           string
	LandsLoneRider bool
	LoneRiderWait  time.Duration
}

// Engine builds trains and takes them a step at a time. Every git command
// runs in the repository's checkout and touches neither its working tree nor
// its branches: a train is built from commits alone and pushed by commit.
// Each step records what is due before it acts, so a step cut short, by a
// restart or a network, is taken on by the next one.
type Engine struct {
	// Commands runs git and gh.
	Commands execx.Runner
	StateDir string
	Now      func() time.Time
	// Wait pauses before GitHub is read again for what a run landed.
	Wait func(time.Duration)
	// TellGoblin types text into a goblin's terminal, and TellCFO reaches
	// the CFO. A nonempty identity denotes a durable one-time notice.
	TellGoblin func(ctx context.Context, task, text string) error
	TellCFO    func(ctx context.Context, identity, text string) error
	// Landed, when set, hears of each pull request the train merged, once it
	// is recorded: AFK mode's log keeps what merged while he is away.
	Landed func(ctx context.Context, t Train, car Car)
	// FailedOnce, when set, hears of the tests a run of the train that
	// passed saw fail once and pass on their second try, once a run: each
	// is a root issue to file.
	FailedOnce func(ctx context.Context, t Train, tests []string)
}

// Start records a train of riders, which Riders put in queue order, builds
// them onto repo's base, pushes them and opens the train's pull request. A
// train already running on repo is refused with ErrRunning. A train none of
// whose riders merges cleanly onto the base stops at once without CI. A
// start cut short is kept, and its next step builds it.
func (e Engine) Start(ctx context.Context, repo Repository, riders []Car) (Train, error) {
	if len(riders) == 0 {
		return Train{}, errors.New("merge train: no pull request rides")
	}
	unlock, err := e.lock(repo.Slug)
	if err != nil {
		return Train{}, err
	}
	defer unlock()
	trains, err := List(e.StateDir)
	if err != nil {
		return Train{}, err
	}
	if running, isRunning := Running(trains, repo.Slug); isRunning {
		return running, fmt.Errorf("%w: %s (%s)", ErrRunning, running.ID, running.PR)
	}
	now := e.Now().UTC()
	stamp := now.Format("20060102-150405")
	t := Train{
		ID:             filepath.Base(repo.Slug) + "-" + stamp,
		Repository:     repo.Slug,
		Checkout:       repo.Checkout,
		Base:           repo.Base,
		Branch:         BranchPrefix + stamp,
		State:          StateTesting,
		LandsLoneRider: repo.LandsLoneRider,
		Started:        now,
	}
	for _, car := range riders {
		car.State = CarRiding
		t.Cars = append(t.Cars, car)
	}
	if err := e.rebuild(ctx, &t, ""); err != nil {
		// What cut the start short is the kept train's first failed step,
		// counted as Advance counts each later one.
		if ctx.Err() == nil && !t.IsFinished() {
			t.Errors = 1
			err = errors.Join(err, e.save(&t))
		}
		return t, err
	}
	switch run := t.openRun(); {
	case t.IsFinished():
	case run != nil && run.OwnRun != "":
		e.tellCFO(ctx, fmt.Sprintf("merge_train: %s train %s lands %s on its own CI run (%s), which tested it merged onto %s at %s, the tree that lands, so no train run tests it again; hold other merges to %s until it lands", t.Repository, t.ID, t.numbers(t.carsIn(CarRiding)), run.OwnRun, t.Base, short(t.BaseSHA), t.Base))
	default:
		e.tellCFO(ctx, fmt.Sprintf("merge_train: %s started train %s with %s onto %s, testing them together in one CI run (%s); hold other merges to %s until it lands", t.Repository, t.ID, t.numbers(t.carsIn(CarRiding)), t.Base, t.PR, t.Base))
	}
	return t, nil
}

// Advance takes the running train id one step. While its CI runs it changes
// nothing. A green run lands its riders, by merging the train's own pull
// request, and then tests the waiting half, if a red run left one, on a pull
// request of its own; a red run has its failed checks run again
// once, and red a second time it halves its riders, or blames the one rider
// it had. A run on a base that moved since is built again on the
// new base and tested again, since what it proved is not what would land.
// A step that fails leaves the train as recorded and counts against it, and
// the train stops once maxErrors steps in a row failed, unless it keeps green
// proof on a merge hold. Another process taking the train's step is ErrBusy.
func (e Engine) Advance(ctx context.Context, id string) (Train, error) {
	t, err := Read(e.StateDir, id)
	if err != nil || t.IsFinished() {
		return t, err
	}
	unlock, err := e.lock(t.Repository)
	if err != nil {
		return t, err
	}
	defer unlock()
	if t, err = Read(e.StateDir, id); err != nil || t.IsFinished() {
		return t, err
	}
	if err := e.notifyMergeHold(ctx, &t); err != nil {
		return t, err
	}
	stepErr := e.step(ctx, &t)
	if stepErr == nil {
		if run := t.openRun(); t.Errors > 0 && (run == nil || run.MergeHold == "") {
			t.Errors = 0
			return t, e.save(&t)
		}
		return t, nil
	}
	if ctx.Err() != nil {
		return t, stepErr
	}
	// The record is what the failed step left saved, not what it changed in
	// memory before it failed.
	kept, err := Read(e.StateDir, id)
	if err != nil || kept.IsFinished() {
		return kept, errors.Join(stepErr, err)
	}
	kept.Errors++
	if run := kept.openRun(); run != nil && len(run.Passed) > 0 && (kept.Errors >= maxErrors || errors.Is(stepErr, errMergeRefused) && strings.Contains(stepErr.Error(), "base branch policy prohibits the merge")) {
		err = e.holdMerge(ctx, &kept, stepErr.Error(), run.MergeReadiness)
		return kept, err
	}
	if kept.Errors < maxErrors {
		return kept, errors.Join(stepErr, e.save(&kept))
	}
	note := fmt.Sprintf("its step failed %d times in a row, last: %v", kept.Errors, stepErr)
	if kept.Landing {
		note += fmt.Sprintf(". It stopped while landing, and its pull request %s lands every pull request of the run or none, so read whether it merged", kept.PR)
	}
	err = e.finish(ctx, &kept, StateFailed, note)
	return kept, err
}

// step takes the train's next step from what its record says.
func (e Engine) step(ctx context.Context, t *Train) error {
	run := t.openRun()
	if t.Landing && len(t.carsIn(CarRiding)) == 0 {
		return e.recordLanding(ctx, t)
	}
	if t.Landing || run != nil && len(run.Passed) > 0 {
		commit := t.Head
		if run != nil && run.OwnRun != "" {
			commit = t.Cars[t.carsIn(CarRiding)[0]].Head
		}
		onBase, err := e.isOnBase(ctx, *t, commit)
		if err != nil {
			return err
		}
		if onBase {
			t.Landing = true
			if err := e.save(t); err != nil {
				return err
			}
			return e.recordLanding(ctx, t)
		}
		t.Landing = false
	}
	if t.Head == "" || t.PR == "" && (run == nil || run.OwnRun == "") {
		return e.rebuild(ctx, t, "")
	}
	outcome, checks, readiness, err := e.ci(ctx, *t)
	if err != nil {
		return err
	}
	if outcome != "passed" && t.DeployHold != nil {
		t.DeployHold, t.Note = nil, ""
		if err := e.save(t); err != nil {
			return err
		}
	}
	if run != nil && len(run.Passed) > 0 && outcome != "passed" && outcome != "closed" {
		changed, err := e.recheck(ctx, t)
		if err != nil {
			return err
		}
		if len(changed) > 0 {
			t.endRun(RunChanged, "")
			return e.rebuild(ctx, t, "the green proof no longer applies, since "+strings.Join(changed, "; "))
		}
		return e.holdMerge(ctx, t, "the checks no longer match the green proof", readiness)
	}
	if outcome == "failed" {
		_, failed := checksOutcome(checks)
		if run != nil {
			run.Failed = failedChecks(failed)
			if len(run.FailedOnce) > 0 {
				run.RetryChecks = checks
			}
			if err := e.save(t); err != nil {
				return err
			}
		}
		if outcome, err = e.secondTry(ctx, t, failed); err != nil {
			return err
		}
	}
	waited := e.Now().Sub(t.Pushed)
	switch {
	case outcome == "closed":
		return e.finish(ctx, t, StateFailed, "its pull request "+t.PR+" was closed or merged by hand")
	case outcome == "stale" && waited >= noCIWithin:
		return e.rebuild(ctx, t, fmt.Sprintf("GitHub did not show the train's head %s within %s of its push, so it was built and pushed again", short(t.Head), noCIWithin))
	case outcome == "none" && waited >= noCIWithin:
		return e.finish(ctx, t, StateFailed, fmt.Sprintf("no CI ran on its pull request %s within %s of the push, or every check was skipped: check the repository's workflows run on pull requests", t.PR, noCIWithin))
	case outcome == "pending" && waited >= RunDeadline:
		return e.finish(ctx, t, StateFailed, fmt.Sprintf("CI on its pull request %s did not finish within %s of the push", t.PR, RunDeadline))
	case outcome == "passed":
		if run != nil {
			if len(run.Passed) == 0 {
				run.Passed = checks
				run.Failed = nil
				if err := e.save(t); err != nil {
					return err
				}
			} else if !sameChecks(run.Passed, checks) {
				return e.holdMerge(ctx, t, "the checks no longer match the green proof", readiness)
			}
		}
		if err := e.noteFailedOnce(ctx, t); err != nil {
			return err
		}
		return e.land(ctx, t, readiness)
	case outcome == "failed":
		_, failed := checksOutcome(checks)
		return e.halve(ctx, t, failed)
	case outcome == "changed":
		return e.rebuild(ctx, t, t.Note)
	}
	return nil
}

// ci reads the CI of the train's pull request at its head: "stale" until
// GitHub shows the head, "none" while no check on it ran, "pending" until
// every check on it concluded, "closed" once the pull request is not open,
// else "passed" or "failed", with the observed checks.
// It also returns GitHub's current merge readiness.
func (e Engine) ci(ctx context.Context, t Train) (string, []Check, string, error) {
	url, head := t.PR, t.Head
	if run := t.openRun(); run != nil && run.OwnRun != "" {
		url, head = run.PR, t.Cars[t.carsIn(CarRiding)[0]].Head
	}
	out, err := e.run(ctx, t.Checkout, "gh", "pr", "view", url, "--json", "state,headRefOid,statusCheckRollup,mergeStateStatus")
	if err != nil {
		return "", nil, "", fmt.Errorf("merge train %s: read its CI: %w", t.ID, err)
	}
	var view struct {
		State      string `json:"state"`
		HeadRefOid string `json:"headRefOid"`
		Checks     Checks `json:"statusCheckRollup"`
		Readiness  string `json:"mergeStateStatus"`
	}
	if err := json.Unmarshal([]byte(out), &view); err != nil {
		return "", nil, "", fmt.Errorf("merge train %s: gh reported its CI in a shape it cannot read: %w", t.ID, err)
	}
	if view.State != "OPEN" && view.State != "CLOSED" && view.State != "MERGED" || view.HeadRefOid == "" {
		return "", nil, view.Readiness, fmt.Errorf("merge train %s: GitHub did not identify its pull request state and head", t.ID)
	}
	if view.State != "OPEN" {
		return "closed", nil, view.Readiness, nil
	}
	if view.HeadRefOid != head {
		return "stale", nil, view.Readiness, nil
	}
	outcome, _ := checksOutcome(view.Checks)
	return outcome, view.Checks, view.Readiness, nil
}

func (e Engine) save(t *Train) error {
	t.Updated = e.Now().UTC()
	return write(e.StateDir, *t)
}

// lock takes the repository's train lock for this process, or says another
// process holds it.
func (e Engine) lock(repository string) (func(), error) {
	dir := Dir(e.StateDir)
	name := strings.ToLower(strings.ReplaceAll(repository, "/", "-")) + ".lock"
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if _, err := lock.AcquireExclusiveNamed(dir, name); err != nil {
		if errors.Is(err, lock.ErrHeld) {
			return nil, fmt.Errorf("%w: %v", ErrBusy, err)
		}
		return nil, err
	}
	return func() { _ = lock.ReleaseExclusiveNamed(dir, name) }, nil
}

// run runs name in dir and returns its trimmed output, or an error naming
// what it printed to stderr.
func (e Engine) run(ctx context.Context, dir, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	result, err := e.Commands.Run(ctx, execx.Request{Dir: dir, Name: name, Args: args})
	if err != nil {
		return "", fmt.Errorf("%s %s: %w", name, args[0], err)
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("%s %s exited %d: %s", name, strings.Join(args[:min(2, len(args))], " "), result.ExitCode, strings.TrimSpace(string(result.Stderr)))
	}
	return strings.TrimSpace(string(result.Stdout)), nil
}

// runWithBody runs gh in dir with args and body, which gh reads from the
// file --body-file names. A body is prose of many lines, and prose on a
// command line is what Microsoft Defender reads as a lure pasted into a
// terminal, so it never goes there.
func (e Engine) runWithBody(ctx context.Context, dir, body string, args ...string) (string, error) {
	file, err := os.CreateTemp("", "cfo-train-body-*.md")
	if err != nil {
		return "", err
	}
	defer os.Remove(file.Name())
	_, err = file.WriteString(body)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", err
	}
	return e.run(ctx, dir, "gh", append(args, "--body-file", file.Name())...)
}

func short(sha string) string {
	return sha[:min(len(sha), 7)]
}
