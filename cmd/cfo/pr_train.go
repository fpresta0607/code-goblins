package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/train"
)

// trainEvery is how often cfo pr train looks at its train's CI, and
// trainStepFailures how many steps in a row may fail before it stops waiting.
const (
	trainEvery        = time.Minute
	trainStepFailures = 5
)

// runPRTrain lands the green pull requests goblins finished in project with
// one CI run: it starts a merge train of them, or joins the one running
// there, and takes it a step every trainEvery until it is over. It exits 0
// once the train landed or stopped at the pull request that broke it, and 1
// when it failed.
func runPRTrain(args []string, stdout, stderr io.Writer, commands execx.Runner, runtime commandRuntime) int {
	fs := flag.NewFlagSet("pr-train", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "cfo pr train: <project> is required")
		return 2
	}
	checkout, err := runtime.resolveProject(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "cfo pr train: %v\n", err)
		return 1
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	ctx := context.Background()
	repository, err := supervisor.TrainRepository(ctx, commands, checkout)
	if err != nil {
		fmt.Fprintf(stderr, "cfo pr train: %v\n", err)
		return 1
	}
	if _, err := afkAuthorityOf(runtime); err != nil {
		fmt.Fprintln(stderr, "cfo pr train: "+err.Error())
		return 1
	}
	engine := train.Engine{
		Commands: commands,
		StateDir: h.State,
		Now:      time.Now,
		Wait:     time.Sleep,
		TellGoblin: func(ctx context.Context, task, text string) error {
			if runtime.sendText == nil {
				return errors.New("this cfo cannot reach a goblin")
			}
			if err := runtime.sendText(ctx, h, task, text); err != nil && !errors.Is(err, fleet.ErrQueuedForToolCall) {
				return err
			}
			return nil
		},
		TellCFO: func(_ context.Context, text string) error {
			_, err := fmt.Fprintln(stdout, text)
			return err
		},
		// While AFK mode is on, what a train merges is logged as merged, with
		// the run that proved it, as cfo pr merge logs its own. The switch is
		// read at each merge, since a train can outlast it.
		Landed: func(_ context.Context, t train.Train, car train.Car) {
			away, err := afkAuthorityOf(runtime)
			if err != nil {
				fmt.Fprintf(stderr, "cfo pr train: %s merged, and %v: log it with cfo afk log --kind merge if AFK mode is on\n", car.URL, err)
				return
			}
			if away == nil {
				return
			}
			if err := away.log(afk.Entry{Kind: afk.KindMerge, What: car.URL, Link: car.URL, Evidence: t.Evidence(), Outcome: afk.OutcomeMerged}); err != nil {
				fmt.Fprintf(stderr, "cfo pr train: AFK mode's log did not take the merge of %s (%v): log it with cfo afk log --kind merge\n", car.URL, err)
			}
		},
	}
	every := runtime.trainEvery
	if every == 0 {
		every = trainEvery
	}
	var t train.Train
	for {
		trains, err := train.List(h.State)
		if err != nil {
			fmt.Fprintf(stderr, "cfo pr train: %v\n", err)
		}
		running, isRunning := train.Running(trains, repository.Slug)
		if isRunning {
			t = running
			fmt.Fprintf(stdout, "train %s is already running on %s (%s); waiting on it\n", t.ID, repository.Slug, t.PR)
			break
		}
		listed, err := ghText(ctx, commands, "pr", "list", "--repo", repository.Slug, "--state", "open", "--base", repository.Base, "--limit", "100", "--json", train.ListFields)
		if err != nil {
			fmt.Fprintf(stderr, "cfo pr train: list the open pull requests of %s: %v\n", repository.Slug, err)
			return 1
		}
		var open []train.PullRequest
		if err := json.Unmarshal([]byte(listed), &open); err != nil {
			fmt.Fprintf(stderr, "cfo pr train: gh listed the open pull requests of %s in a shape it cannot read: %v\n", repository.Slug, err)
			return 1
		}
		viewer, err := ghText(ctx, commands, "api", "user", "--jq", ".login")
		if err != nil {
			fmt.Fprintf(stderr, "cfo pr train: read the account gh works as: %v\n", err)
			return 1
		}
		riders, left := train.Riders(open, repository.Base, viewer, supervisor.TrainGoblins(h.State, checkout), trains)
		for _, why := range left {
			fmt.Fprintln(stdout, "stays off: "+why)
		}
		if len(riders) == 0 {
			fmt.Fprintf(stdout, "no green pull request a goblin finished waits on %s, so no train starts\n", repository.Base)
			return 0
		}
		t, err = engine.Start(ctx, repository, riders)
		switch {
		case errors.Is(err, train.ErrBusy) || errors.Is(err, train.ErrRunning):
			// The supervisor is starting a train on this repository at this
			// very moment: this one waits on it instead.
			time.Sleep(every)
			continue
		case err != nil && t.ID == "":
			fmt.Fprintf(stderr, "cfo pr train: %v\n", err)
			return 1
		case err != nil:
			fmt.Fprintf(stderr, "cfo pr train: %v; train %s is kept, and its next step builds it\n", err, t.ID)
		}
		break
	}
	said, failures := "", 0
	for !t.IsFinished() {
		if line := trainProgress(t); line != said && t.Head != "" {
			fmt.Fprintln(stdout, line)
			said = line
		}
		time.Sleep(every)
		next, err := engine.Advance(ctx, t.ID)
		if errors.Is(err, train.ErrBusy) {
			next, err = train.Read(h.State, t.ID)
		}
		if err != nil {
			// A failed step leaves the train as recorded and counts against
			// it, and the train stops itself once too many fail in a row.
			failures++
			fmt.Fprintf(stderr, "cfo pr train: %v; the train is kept for its next step\n", err)
			if failures > trainStepFailures {
				fmt.Fprintf(stderr, "cfo pr train: %d steps in a row failed; the supervisor keeps moving train %s, and cfo pr train %s waits on it again\n", failures, t.ID, fs.Arg(0))
				return 1
			}
			if next.ID == "" {
				continue
			}
		} else {
			failures = 0
		}
		t = next
	}
	if t.State == train.StateFailed {
		return 1
	}
	return 0
}

// trainProgress says in one line what a running train is testing.
func trainProgress(t train.Train) string {
	var riding []string
	for _, car := range t.Cars {
		if car.State == train.CarRiding {
			riding = append(riding, fmt.Sprintf("#%d", car.Number))
		}
	}
	line := fmt.Sprintf("run %d of train %s: CI tests %s on %s at %s (%s)", t.Runs, t.ID, strings.Join(riding, ", "), t.Base, t.BaseSHA[:min(len(t.BaseSHA), 7)], t.PR)
	if t.Note != "" {
		line += "; " + t.Note
	}
	return line
}
