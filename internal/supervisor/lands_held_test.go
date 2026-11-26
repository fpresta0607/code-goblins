package supervisor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/githubtest"
	"github.com/fpresta0607/code-goblins/internal/train"
)

// Issue 2724: the gate of a root pickup opens its pull request with no
// label, so in a project whose record says its pull requests land held, the
// supervisor's train takes none that never carried the hold label, however
// green and however reported done. One its owner held and then took the
// label off rides as before.
func TestTheSupervisorsTrainTakesNoRootPickupsPullRequestThatNeverCarriedHoldInAHeldProject(t *testing.T) {
	for _, test := range []struct {
		name           string
		hasCarriedHold bool
		isRiding       bool
	}{
		{name: "never held"},
		{name: "held, then let go", hasCarriedHold: true, isRiding: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			service, h := fleetService(t)
			project := newTrainProject(t)
			dir := filepath.Join(h.Data, "projects", filepath.Base(project.checkout))
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "project.json"), []byte(`{"project": "app", "delivery": {"pull_requests_land_held": true}}`), 0o600); err != nil {
				t.Fatal(err)
			}
			first, second := project.pull(7), project.pull(8)
			first.HasCarriedHold, second.HasCarriedHold = test.hasCarriedHold, test.hasCarriedHold
			reportDone(t, h, "root-app-7", project.checkout, "done: PR "+first.URL)
			reportDone(t, h, "root-app-8", project.checkout, "done: PR "+second.URL)

			// Act
			err := service.runTrain(context.Background(), project, githubtest.Client(project), &fleetWakes{}, project.checkout, []train.PullRequest{first, second})

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			trains, err := train.List(h.State)
			if err != nil || (len(trains) == 1) != test.isRiding || (project.created == 1) != test.isRiding {
				t.Fatalf("trains = %+v, %v, train pull requests opened %d: want a train %v", trains, err, project.created, test.isRiding)
			}
		})
	}
}

// Issue 2724: the CI poll reads the held declaration too, so a goblin that
// reported done a green pull request no train takes in a held project wakes
// the CEO naming why it waits and the command that holds it.
func TestTheCIPollSaysAHeldProjectsPullRequestThatNeverCarriedHoldWaits(t *testing.T) {
	// Arrange
	s, h, forge := waitingFleet(t, pull209(false))
	writeFile(t, filepath.Join(h.Data, "projects", filepath.Base(forge.repo), "project.json"), `{"project": "app", "delivery": {"pull_requests_land_held": true}}`)
	report(t, h, ceoWaitStarted, donePull209)
	now := ceoWaitStarted

	// Act
	woke := sweepUntil(t, s, h, &now, ceoWaitStarted.Add(15*time.Minute))

	// Assert
	if len(woke) != 1 {
		t.Fatalf("the wait woke the CEO %d times, want once: %+v", len(woke), woke)
	}
	for _, want := range []string{donePull209 + ", which no merge train takes: its project's pull requests land held and it never carried the hold label", "gh pr edit https://github.com/o/r/pull/209 --add-label hold"} {
		if !strings.Contains(woke[0].Detail, want) {
			t.Errorf("wake %q lacks %q", woke[0].Detail, want)
		}
	}
}
