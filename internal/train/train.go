// Package train lands several green pull requests on a repository's default
// branch with one CI run: a merge train.
//
// Landing pull requests one at a time costs one CI run each, in a row, since
// every merge makes the other green runs stale. A train merges the green ones
// onto main in queue order on a branch of its own, opens a pull request for
// that branch that is never merged, and lets CI test the combination once.
// When the run is green each pull request merges with a merge commit in the
// same order, and main's tree must then equal the train's. When it is red the
// train is halved until the one pull request that breaks it is found; every
// half that passes lands on the way.
package train

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/goblinname"
)

// Schema names the shape of a train's record.
const Schema = "cfo-merge-train.v1"

// BranchPrefix starts the name of every train's branch. A pull request from
// such a branch is a train's own and never rides another.
const BranchPrefix = "cfo/train-"

// HoldLabel is the label that keeps a pull request off every train: one the
// CFO must not merge on its own (recovery, security, money paths) or one the
// Overlord said to wait on.
const HoldLabel = "hold"

// keepFor is how long a finished train's record is kept: the board shows it,
// and a pull request it found broken does not ride again at the same head.
const keepFor = 7 * 24 * time.Hour

// A train's states.
const (
	// StateTesting is a train whose pull request CI is testing.
	StateTesting = "testing"
	// StateLanded is a finished train whose every tested pull request landed.
	StateLanded = "landed"
	// StateStopped is a finished train that found the pull request that
	// breaks it, or had none left that merged cleanly; what passed landed.
	StateStopped = "stopped"
	// StateFailed is a train a step could not take: its note says what and
	// what the CFO does next.
	StateFailed = "failed"
)

// A car's states.
const (
	// CarRiding is in the run CI is testing.
	CarRiding = "riding"
	// CarWaiting was in a red run and waits for its own half's run.
	CarWaiting = "waiting"
	// CarLanded is merged into the base.
	CarLanded = "landed"
	// CarCulprit failed CI alone on the base: the pull request that broke
	// the train.
	CarCulprit = "culprit"
	// CarConflict did not merge cleanly onto the base with the cars ahead of
	// it, so it did not ride.
	CarConflict = "conflict"
	// CarReturned left the train untested and waits for the next one.
	CarReturned = "returned"
)

// Train is one merge train, as its record keeps it.
type Train struct {
	Schema string `json:"schema"`
	ID     string `json:"id"`
	// Repository is the GitHub repository, as owner/name.
	Repository string `json:"repository"`
	// Checkout is the local clone the train is built in.
	Checkout string `json:"checkout"`
	// Base is the branch the pull requests merge into.
	Base string `json:"base"`
	// Branch is the train's own branch, and PR its pull request.
	Branch string `json:"branch"`
	PR     string `json:"pr,omitempty"`
	State  string `json:"state"`
	// BaseSHA is the base commit the run CI tests was built on, Head that
	// run's commit, and Pushed when it was pushed. No head means the next
	// run is still to be built.
	BaseSHA string    `json:"base_sha,omitempty"`
	Head    string    `json:"head,omitempty"`
	Pushed  time.Time `json:"pushed,omitzero"`
	// Runs counts the CI runs the train started.
	Runs int `json:"runs"`
	// Landing says the run passed and its riders are being merged, so a
	// step cut short in the middle merges the rest rather than reading CI.
	Landing bool `json:"landing,omitempty"`
	// Moved counts the runs in a row the base moved during, and Errors the
	// steps in a row that failed.
	Moved    int       `json:"moved,omitempty"`
	Errors   int       `json:"errors,omitempty"`
	Cars     []Car     `json:"cars"`
	Started  time.Time `json:"started"`
	Updated  time.Time `json:"updated"`
	Finished time.Time `json:"finished,omitzero"`
	// Note says what happened last in words: why a run restarted, why the
	// train stopped or failed, and what to do about it.
	Note string `json:"note,omitempty"`
}

// Car is one pull request on a train.
type Car struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	Title  string `json:"title"`
	Branch string `json:"branch"`
	// Head is the commit that rides: the pull request merges only while its
	// head is still this commit.
	Head string `json:"head"`
	// Task is the goblin that reported the pull request done, and Goblin
	// and GoblinTitle its fun name and title.
	Task        string `json:"task"`
	Goblin      string `json:"goblin,omitempty"`
	GoblinTitle string `json:"goblin_title,omitempty"`
	State       string `json:"state"`
	Note        string `json:"note,omitempty"`
}

// IsFinished says whether the train is over.
func (t Train) IsFinished() bool {
	return t.State != StateTesting
}

// Evidence says what proved the pull requests a run of the train lands: the
// run CI passed on and the base it was built on.
func (t Train) Evidence() string {
	return fmt.Sprintf("merge train %s: CI passed on %s at %s, built on %s at %s", t.ID, t.PR, short(t.Head), t.Base, short(t.BaseSHA))
}

// carsIn returns the indexes of the train's cars in state, in train order.
func (t Train) carsIn(state string) []int {
	var found []int
	for i, car := range t.Cars {
		if car.State == state {
			found = append(found, i)
		}
	}
	return found
}

// riders names the cars at indexes with their goblins, as "#1 from Jerry
// (cg-x), #2 from Mo (cg-y)".
func (t Train) riders(indexes []int) string {
	names := make([]string, 0, len(indexes))
	for _, i := range indexes {
		names = append(names, fmt.Sprintf("#%d from %s", t.Cars[i].Number, goblinname.Called(t.Cars[i].Goblin, t.Cars[i].Task)))
	}
	return strings.Join(names, ", ")
}

// numbers names the cars at indexes as "#1, #2".
func (t Train) numbers(indexes []int) string {
	names := make([]string, 0, len(indexes))
	for _, i := range indexes {
		names = append(names, fmt.Sprintf("#%d", t.Cars[i].Number))
	}
	return strings.Join(names, ", ")
}

// Dir is where trains' records are kept in a state directory.
func Dir(stateDir string) string {
	return filepath.Join(stateDir, "trains")
}

func recordPath(stateDir, id string) string {
	return filepath.Join(Dir(stateDir), id+".json")
}

// Read reads the train id's record.
func Read(stateDir, id string) (Train, error) {
	data, err := fsx.ReadFile(recordPath(stateDir, id))
	if err != nil {
		return Train{}, fmt.Errorf("merge train %s: %w", id, err)
	}
	var t Train
	if err := json.Unmarshal(data, &t); err != nil || t.Schema != Schema {
		return Train{}, fmt.Errorf("merge train %s: its record %s cannot be read", id, recordPath(stateDir, id))
	}
	return t, nil
}

func write(stateDir string, t Train) error {
	t.Schema = Schema
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(Dir(stateDir), 0o700); err != nil {
		return err
	}
	return fsx.AtomicWriteFile(recordPath(stateDir, t.ID), data)
}

// List reads every train kept in stateDir, the newest first. A record that
// cannot be read is left out, and the error names it.
func List(stateDir string) ([]Train, error) {
	entries, err := os.ReadDir(Dir(stateDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var (
		trains []Train
		errs   error
	)
	for _, entry := range entries {
		id, isRecord := strings.CutSuffix(entry.Name(), ".json")
		if entry.IsDir() || !isRecord {
			continue
		}
		t, err := Read(stateDir, id)
		if err != nil {
			errs = errors.Join(errs, err)
			continue
		}
		trains = append(trains, t)
	}
	slices.SortFunc(trains, func(a, b Train) int { return b.Started.Compare(a.Started) })
	return trains, errs
}

// Running returns repository's train that is still running, if one is.
func Running(trains []Train, repository string) (Train, bool) {
	for _, t := range trains {
		if !t.IsFinished() && strings.EqualFold(t.Repository, repository) {
			return t, true
		}
	}
	return Train{}, false
}

// Prune removes the records of trains that finished more than keepFor
// before now.
func Prune(stateDir string, now time.Time) error {
	trains, err := List(stateDir)
	for _, t := range trains {
		if t.IsFinished() && now.Sub(t.Finished) >= keepFor {
			err = errors.Join(err, fsx.Remove(recordPath(stateDir, t.ID)))
		}
	}
	return err
}
