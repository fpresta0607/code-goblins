package supervisor

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/crewstate"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/train"
)

// trainsShownFor is how long the board keeps showing a finished train.
const trainsShownFor = 6 * time.Hour

// minimumRiders is how many green goblin pull requests waiting on one base
// start a train by themselves: one alone is merged as it always was.
const minimumRiders = 2

// TrainRepository reads the GitHub repository a checkout's origin is and its
// default branch, as the CI poll reads them, for a merge train.
func TrainRepository(ctx context.Context, runner execx.Runner, checkout string) (train.Repository, error) {
	origin, err := runOutput(ctx, runner, checkout, "git", "config", "--get", "remote.origin.url")
	if err != nil {
		return train.Repository{}, fmt.Errorf("merge train: read the origin of %s: %w", checkout, err)
	}
	remote := githubRemote.FindStringSubmatch(origin)
	if remote == nil {
		return train.Repository{}, fmt.Errorf("merge train: the origin of %s is not on GitHub", checkout)
	}
	base, err := defaultBranch(ctx, runner, checkout)
	if err != nil {
		return train.Repository{}, err
	}
	return train.Repository{Slug: remote[1], Checkout: checkout, Base: base}, nil
}

// TrainGoblins reads the live goblins working in checkout and, for each, the
// pull requests it reported done since its latest other report: a goblin
// that reported anything after its done report is working again, and its
// pull request does not ride until it reports done again.
func TrainGoblins(stateDir, checkout string) []train.Goblin {
	var goblins []train.Goblin
	for _, meta := range liveTasks(stateDir) {
		if meta.Project == "" || !fsx.SamePath(meta.Project, checkout) {
			continue
		}
		lines, _ := state.TailStatus(stateDir, meta.ID, 200)
		if done := doneReports(lines, spawnTime(meta.SpawnGen)); len(done) > 0 {
			goblins = append(goblins, train.Goblin{Task: meta.ID, Done: done})
		}
	}
	return goblins
}

// doneReports reads a goblin's status log back from its newest report while
// the reports are done reports, and returns each pull request they name with
// when it was first reported done in that run, so reporting it again keeps
// its place in the queue.
func doneReports(lines []string, spawned time.Time) map[string]time.Time {
	done := map[string]time.Time{}
	for i := len(lines) - 1; i >= 0; i-- {
		stamp, event := state.SplitStatus(lines[i])
		if !spawned.IsZero() && stamp.Before(spawned.Truncate(time.Second)) {
			break
		}
		event = strings.TrimSpace(event)
		if event == "" || crewstate.IsCFOAudit(event) {
			continue
		}
		rest, isDone := strings.CutPrefix(event, "done: PR ")
		if !isDone {
			break
		}
		if fields := strings.Fields(rest); len(fields) > 0 && githubPullRequest.MatchString(fields[0]) {
			done[fields[0]] = stamp
		}
	}
	return done
}

// runTrain takes the merge train running on repo a step, or starts one when
// at least minimumRiders goblins' finished pull requests wait green on its
// default branch. open is the pull request list the poll read, nil when it
// could not be read: a running train still takes its step, and none starts.
func (s *Service) runTrain(ctx context.Context, runner execx.Runner, repo string, open []train.PullRequest) error {
	stateDir := s.Store.Home.State
	repository, err := TrainRepository(ctx, runner, repo)
	if err != nil {
		return err
	}
	trains, listErr := train.List(stateDir)
	engine := s.trainEngine(runner, repository.Slug)
	if running, isRunning := train.Running(trains, repository.Slug); isRunning {
		_, err := engine.Advance(ctx, running.ID)
		if errors.Is(err, train.ErrBusy) {
			err = nil
		}
		return errors.Join(listErr, err)
	}
	goblins := TrainGoblins(stateDir, repo)
	if len(doneAndOpen(goblins, open)) < minimumRiders {
		return listErr
	}
	viewer, err := runOutput(ctx, runner, repo, "gh", "api", "user", "--jq", ".login")
	if err != nil {
		return errors.Join(listErr, fmt.Errorf("merge train: read the account gh works as in %s: %w", repo, err))
	}
	riders, _ := train.Riders(open, repository.Base, viewer, goblins, trains)
	if len(riders) < minimumRiders {
		return listErr
	}
	_, err = engine.Start(ctx, repository, riders)
	if errors.Is(err, train.ErrBusy) || errors.Is(err, train.ErrRunning) {
		err = nil
	}
	return errors.Join(listErr, err)
}

// advanceUnwatchedTrains takes a step for each running train in a checkout
// the poll no longer watches, since no live goblin works there any more: a
// train outlives the goblins whose pull requests ride it.
func (s *Service) advanceUnwatchedTrains(ctx context.Context, runner execx.Runner, w *fleetWakes, watched []string, currentTime func() time.Time) error {
	trains, err := train.List(s.Store.Home.State)
	for _, t := range trains {
		if t.IsFinished() || currentTime().Before(w.BackOff[t.Checkout]) || slices.ContainsFunc(watched, func(repo string) bool { return fsx.SamePath(repo, t.Checkout) }) {
			continue
		}
		pollRunner := githubPollRunner{commands: runner, state: w, repo: t.Checkout, now: currentTime}
		if _, stepErr := s.trainEngine(pollRunner, t.Repository).Advance(ctx, t.ID); !errors.Is(stepErr, train.ErrBusy) {
			err = errors.Join(err, stepErr)
		}
	}
	return err
}

// doneAndOpen names the open pull requests a goblin reported done, so the
// account gh works as is asked for only when enough of them wait to ride.
func doneAndOpen(goblins []train.Goblin, open []train.PullRequest) []string {
	var urls []string
	for _, pr := range open {
		if slices.ContainsFunc(goblins, func(goblin train.Goblin) bool { _, isDone := goblin.Done[pr.URL]; return isDone }) {
			urls = append(urls, pr.URL)
		}
	}
	return urls
}

// trainEngine is the merge train engine as the supervisor runs it: it tells
// a goblin through its own native terminal and the CFO with a pr wake.
func (s *Service) trainEngine(runner execx.Runner, repository string) train.Engine {
	stateDir := s.Store.Home.State
	return train.Engine{
		Commands: runner,
		StateDir: stateDir,
		Now:      time.Now,
		Wait:     time.Sleep,
		TellGoblin: func(ctx context.Context, task, text string) error {
			if s.Options.CFO == nil {
				return errors.New("this supervisor has no way to reach a goblin")
			}
			meta, err := state.ReadTaskMeta(stateDir, task)
			if err != nil {
				return err
			}
			_, err = s.Options.CFO.SendGoblin(ctx, task, goblinIdentity(meta), text)
			if errors.Is(err, fleet.ErrQueuedForToolCall) {
				return nil
			}
			return err
		},
		TellCFO: func(_ context.Context, text string) error {
			return raiseFleetWake(stateDir, "pr", "train:"+repository, text)
		},
		Landed: func(_ context.Context, t train.Train, car train.Car) {
			if !s.afkOn() {
				return
			}
			entry := afk.Entry{Kind: afk.KindMerge, What: car.URL, Link: car.URL, Evidence: t.Evidence(), Outcome: afk.OutcomeMerged}
			if err := s.logAFKDecision(entry); err != nil {
				_ = raiseFleetWake(stateDir, "pr", "train:"+repository, fmt.Sprintf("merge_train: AFK mode's log did not take the merge of %s by train %s (%v); log it with cfo afk log --kind merge", car.URL, t.ID, err))
			}
		},
	}
}

// carriedByTrains names the pull requests a running merge train carries.
func carriedByTrains(stateDir string) map[string]bool {
	carried := map[string]bool{}
	trains, _ := train.List(stateDir)
	for _, t := range trains {
		for _, car := range t.Cars {
			if !t.IsFinished() && (car.State == train.CarRiding || car.State == train.CarWaiting) {
				carried[car.URL] = true
			}
		}
	}
	return carried
}

// keepTrains keeps the trains the board shows: every running one, and each
// finished one for trainsShownFor. It forgets trains kept past their time.
func (s *Service) keepTrains(now time.Time) error {
	stateDir := s.Store.Home.State
	pruneErr := train.Prune(stateDir, now)
	trains, err := train.List(stateDir)
	shown := []train.Train{}
	for _, t := range trains {
		if !t.IsFinished() || now.Sub(t.Finished) < trainsShownFor {
			shown = append(shown, t)
		}
	}
	s.mu.Lock()
	s.trains = shown
	s.mu.Unlock()
	return errors.Join(pruneErr, err)
}
