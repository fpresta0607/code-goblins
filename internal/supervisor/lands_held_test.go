package supervisor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

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
