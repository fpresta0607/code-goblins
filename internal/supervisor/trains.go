package supervisor

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
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
// pull requests it reported done in its current run, whatever it reported
// after: a goblin that goes on to its next pull request, or is blocked on
// it, has still finished this one, and whether it rides is the pull
// request's own state, open, green and not in conflict, which train.Riders
// reads. The whole log is read, since a long run's done report can lie
// further back than any tail of it.
func TrainGoblins(stateDir, checkout string) []train.Goblin {
	var goblins []train.Goblin
	for _, meta := range liveTasks(stateDir) {
		if meta.Project == "" || !fsx.SamePath(meta.Project, checkout) {
			continue
		}
		lines, _ := state.TailStatus(stateDir, meta.ID, math.MaxInt)
		if done := doneReports(lines, spawnTime(meta.SpawnGen)); len(done) > 0 {
			goblins = append(goblins, train.Goblin{Task: meta.ID, Name: meta.GoblinName, Title: meta.GoblinTitle, Done: done})
		}
	}
	return goblins
}

// doneReports returns each pull request a goblin's status log reports done
// since it was spawned, with when it was first reported done in that run, so
// reporting it again keeps its place in the queue and nothing reported after
// takes it away.
func doneReports(lines []string, spawned time.Time) map[string]time.Time {
	done := map[string]time.Time{}
	for _, line := range lines {
		stamp, event := state.SplitStatus(line)
		if !spawned.IsZero() && stamp.Before(spawned.Truncate(time.Second)) {
			continue
		}
		rest, isDone := strings.CutPrefix(strings.TrimSpace(event), "done: PR ")
		if !isDone {
			continue
		}
		if fields := strings.Fields(rest); len(fields) > 0 && githubPullRequest.MatchString(fields[0]) {
			if _, seen := done[fields[0]]; !seen {
				done[fields[0]] = stamp
			}
		}
	}
	return done
}

// runTrain takes the merge train running on repo a step, or starts one when
// at least minimumRiders goblins' finished pull requests wait green on its
// default branch. open is the pull request list the poll read, nil when it
// could not be read: a running train still takes its step, and none starts.
// A step or a read of the account gh works as that fails is tried again on
// the next poll, and returned only once it failed failingPasses polls in a
// row.
func (s *Service) runTrain(ctx context.Context, runner execx.Runner, w *fleetWakes, repo string, open []train.PullRequest) error {
	stateDir := s.Store.Home.State
	repository, err := TrainRepository(ctx, runner, repo)
	if err != nil {
		return err
	}
	trains, listErr := train.List(stateDir)
	engine := s.trainEngine(runner, repository.Slug)
	if running, isRunning := train.Running(trains, repository.Slug); isRunning {
		return errors.Join(listErr, advanceTrain(ctx, engine, running.ID))
	}
	goblins := TrainGoblins(stateDir, repo)
	if len(doneAndOpen(goblins, open)) < minimumRiders {
		return listErr
	}
	viewer, err := runOutput(ctx, runner, repo, "gh", "api", "user", "--jq", ".login")
	if err != nil {
		return errors.Join(listErr, w.failing("train viewer:"+repo, fmt.Errorf("merge train: read the account gh works as in %s: %w", repo, err)))
	}
	delete(w.Failing, "train viewer:"+repo)
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
		err = errors.Join(err, advanceTrain(ctx, s.trainEngine(pollRunner, t.Repository), t.ID))
	}
	return err
}

// advanceTrain takes train id's next step and returns what went wrong, but
// not a step another poll is taking, nor one that failed on fewer than
// failingPasses polls in a row: the train counts its own failed steps and
// takes the step again on the next poll, and on 2026-10-08 a read that timed
// out under the fleet's load took about 600 ms minutes later.
func advanceTrain(ctx context.Context, engine train.Engine, id string) error {
	advanced, err := engine.Advance(ctx, id)
	if errors.Is(err, train.ErrBusy) || err != nil && !advanced.IsFinished() && advanced.Errors > 0 && advanced.Errors < failingPasses {
		return nil
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

// MergeTrainView is a merge train as the board shows it: one card for each
// batch of pull requests, so it carries the trains before it that landed
// nothing and whose pull requests it took on, oldest first.
type MergeTrainView struct {
	train.Train
	Earlier []train.Train `json:"earlier,omitempty"`
}

// trainBatches folds each finished train that landed nothing into the next
// train of its repository that took one of its pull requests on, and keeps
// every other train as it is. trains come newest first, as train.List reads
// them, and so do the batches.
func trainBatches(trains []train.Train) []MergeTrainView {
	var batches []MergeTrainView
	for _, t := range slices.Backward(trains) {
		batch := MergeTrainView{Train: t}
		batches = slices.DeleteFunc(batches, func(before MergeTrainView) bool {
			if !strings.EqualFold(before.Repository, t.Repository) || landedAny(before.Train) || !takesOn(t, before.Train) {
				return false
			}
			batch.Earlier = append(batch.Earlier, append(before.Earlier, before.Train)...)
			return true
		})
		batches = append(batches, batch)
	}
	slices.Reverse(batches)
	return batches
}

// landedAny says whether any of t's pull requests landed.
func landedAny(t train.Train) bool {
	return slices.ContainsFunc(t.Cars, func(car train.Car) bool { return car.State == train.CarLanded })
}

// takesOn says whether later carries any of earlier's pull requests.
func takesOn(later, earlier train.Train) bool {
	return slices.ContainsFunc(later.Cars, func(car train.Car) bool {
		return slices.ContainsFunc(earlier.Cars, func(before train.Car) bool { return before.URL == car.URL })
	})
}

// keepTrains keeps the trains the board shows, one for each batch: every
// running one, and each finished one for trainsShownFor. It forgets trains
// kept past their time.
func (s *Service) keepTrains(now time.Time) error {
	stateDir := s.Store.Home.State
	pruneErr := train.Prune(stateDir, now)
	trains, err := train.List(stateDir)
	shown := []MergeTrainView{}
	for _, batch := range trainBatches(trains) {
		if !batch.IsFinished() || now.Sub(batch.Finished) < trainsShownFor {
			shown = append(shown, batch)
		}
	}
	s.mu.Lock()
	s.trains = shown
	s.mu.Unlock()
	return errors.Join(pruneErr, err)
}
