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
	// mergeAttempts bounds how often a merge GitHub refused because the base
	// moved under it is tried, and mergeRetryAfter is the pause between.
	mergeAttempts   = 3
	mergeRetryAfter = 5 * time.Second
)

var (
	// ErrRunning says a train is already running on the repository.
	ErrRunning = errors.New("a merge train is already running on this repository")
	// ErrBusy says another process is taking the train's next step.
	ErrBusy = errors.New("another process is moving this merge train")
)

// Repository is where a train runs: the GitHub repository as owner/name, a
// local clone of it whose origin is that repository, and the branch pull
// requests merge into.
type Repository struct {
	Slug     string
	Checkout string
	Base     string
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
	// Wait pauses before a merge is tried again.
	Wait func(time.Duration)
	// TellGoblin types text into a goblin's terminal, and TellCFO reaches
	// the CFO.
	TellGoblin func(ctx context.Context, task, text string) error
	TellCFO    func(ctx context.Context, text string) error
	// Landed, when set, hears of each pull request the train merged, once it
	// is recorded: AFK mode's log keeps what merged while he is away.
	Landed func(ctx context.Context, t Train, car Car)
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
		ID:         filepath.Base(repo.Slug) + "-" + stamp,
		Repository: repo.Slug,
		Checkout:   repo.Checkout,
		Base:       repo.Base,
		Branch:     BranchPrefix + stamp,
		State:      StateTesting,
		Started:    now,
	}
	for _, car := range riders {
		car.State = CarRiding
		t.Cars = append(t.Cars, car)
	}
	if err := e.rebuild(ctx, &t, ""); err != nil {
		return t, err
	}
	if !t.IsFinished() {
		e.tellCFO(ctx, fmt.Sprintf("merge_train: %s started train %s with %s onto %s, testing them together in one CI run (%s); hold other merges to %s until it lands", t.Repository, t.ID, t.numbers(t.carsIn(CarRiding)), t.Base, t.PR, t.Base))
	}
	return t, nil
}

// Advance takes the running train id one step. While its CI runs it changes
// nothing. A green run lands its riders in order and then tests the waiting
// half, if a red run left one; a red run has its failed checks run again
// once, and red a second time it halves its riders, or blames the one rider
// it had. A run on a base that moved since is built again on the
// new base and tested again, since what it proved is not what would land.
// A step that fails leaves the train as recorded and counts against it, and
// the train stops once maxErrors steps in a row failed. Another process
// taking the train's step is ErrBusy.
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
	stepErr := e.step(ctx, &t)
	if stepErr == nil {
		if t.Errors > 0 {
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
	if kept.Errors < maxErrors {
		return kept, errors.Join(stepErr, e.save(&kept))
	}
	note := fmt.Sprintf("its step failed %d times in a row, last: %v", kept.Errors, stepErr)
	if kept.Landing {
		note += fmt.Sprintf("; it stopped while landing, so %s may hold only part of what CI tested, and %s's own push CI decides", kept.Base, kept.Base)
	}
	err = e.finish(ctx, &kept, StateFailed, note)
	return kept, err
}

// step takes the train's next step from what its record says.
func (e Engine) step(ctx context.Context, t *Train) error {
	if t.Landing {
		return e.land(ctx, t)
	}
	if t.Head == "" || t.PR == "" {
		return e.rebuild(ctx, t, "")
	}
	outcome, failed, err := e.ci(ctx, *t)
	if err != nil {
		return err
	}
	if outcome == "failed" {
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
		return e.finish(ctx, t, StateFailed, fmt.Sprintf("no CI ran on its pull request %s within %s of the push: check the repository's workflows run on pull requests", t.PR, noCIWithin))
	case outcome == "pending" && waited >= RunDeadline:
		return e.finish(ctx, t, StateFailed, fmt.Sprintf("CI on its pull request %s did not finish within %s of the push", t.PR, RunDeadline))
	case outcome == "passed":
		return e.land(ctx, t)
	case outcome == "failed":
		return e.halve(ctx, t, failed)
	}
	return nil
}

// ci reads the CI of the train's pull request at its head: "stale" until
// GitHub shows the head, "none" while it has no checks, "pending" until
// every check on it concluded, "closed" once the pull request is not open,
// else "passed" or "failed" with the checks that failed.
func (e Engine) ci(ctx context.Context, t Train) (string, []Check, error) {
	out, err := e.run(ctx, t.Checkout, "gh", "pr", "view", t.PR, "--json", "state,headRefOid,statusCheckRollup")
	if err != nil {
		return "", nil, fmt.Errorf("merge train %s: read its CI: %w", t.ID, err)
	}
	var view struct {
		State      string  `json:"state"`
		HeadRefOid string  `json:"headRefOid"`
		Checks     []Check `json:"statusCheckRollup"`
	}
	if err := json.Unmarshal([]byte(out), &view); err != nil {
		return "", nil, fmt.Errorf("merge train %s: gh reported its CI in a shape it cannot read: %w", t.ID, err)
	}
	if view.State != "OPEN" {
		return "closed", nil, nil
	}
	if view.HeadRefOid != t.Head {
		return "stale", nil, nil
	}
	outcome, failed := checksOutcome(view.Checks)
	return outcome, failed, nil
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

func short(sha string) string {
	return sha[:min(len(sha), 7)]
}
