package supervisor

import (
	"context"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/train"
)

func TestSupervisorRestartsKeepAGreenRefusedTrainWithoutDuplicateCI(t *testing.T) {
	// Arrange
	service, h := fleetService(t)
	project := newTrainProject(t)
	first, second := project.pull(11), project.pull(12)
	reportDone(t, h, "g11", project.checkout, "done: PR "+first.URL)
	reportDone(t, h, "g12", project.checkout, "done: PR "+second.URL)
	if err := service.runTrain(context.Background(), project, &fleetWakes{}, project.checkout, project.pulls); err != nil {
		t.Fatal(err)
	}
	started, err := train.List(h.State)
	if err != nil || len(started) != 1 {
		t.Fatalf("trains %+v, error %v: want one train", started, err)
	}
	project.mergeRefusal = "the base branch policy prohibits the merge"

	// Act: discard each Service and its in-memory poll state.
	for range 8 {
		restarted := &Service{Store: service.Store}
		if err := restarted.runTrain(context.Background(), project, &fleetWakes{}, project.checkout, project.pulls); err != nil {
			t.Fatal(err)
		}
	}
	kept, err := train.List(h.State)

	// Assert
	if err != nil || len(kept) != 1 || kept[0].IsFinished() || kept[0].Head != started[0].Head || kept[0].BaseSHA != started[0].BaseSHA || kept[0].PR != started[0].PR || kept[0].Errors != 1 || len(kept[0].History[0].Passed) != 1 {
		t.Fatalf("trains %+v, error %v: want the one green train and its spent error budget", kept, err)
	}
	if project.created != 1 || project.trainPushes != 1 || project.mergeAttempts != 1 || project.closed || project.reruns != 0 {
		t.Fatalf("created %d, pushes %d, merges %d, closed %v, reruns %d: want no duplicate compute or cleanup", project.created, project.trainPushes, project.mergeAttempts, project.closed, project.reruns)
	}
	if notices := prWakes(t, h, "merge_train"); len(notices) != 2 {
		t.Fatalf("notices %+v: want one start and one durable hold", notices)
	}
}

func TestSupervisorNeverRetriesCancelledCIForHeldOrClosedRiders(t *testing.T) {
	for _, isClosed := range []bool{false, true} {
		t.Run(map[bool]string{false: "held", true: "closed"}[isClosed], func(t *testing.T) {
			// Arrange
			service, h := fleetService(t)
			project := newTrainProject(t)
			first, second := project.pull(11), project.pull(12)
			reportDone(t, h, "g11", project.checkout, "done: PR "+first.URL)
			reportDone(t, h, "g12", project.checkout, "done: PR "+second.URL)
			if err := service.runTrain(context.Background(), project, &fleetWakes{}, project.checkout, project.pulls); err != nil {
				t.Fatal(err)
			}
			project.ciConclusion = "CANCELLED"
			if isClosed {
				project.pulls = nil
			} else {
				for index := range project.pulls {
					project.pulls[index].Labels = []train.Label{{Name: train.HoldLabel}}
				}
			}

			// Act
			for range 3 {
				restarted := &Service{Store: service.Store}
				if err := restarted.runTrain(context.Background(), project, &fleetWakes{}, project.checkout, project.pulls); err != nil {
					t.Fatal(err)
				}
			}
			kept, err := train.List(h.State)

			// Assert
			if err != nil || len(kept) != 1 || project.created != 1 || project.trainPushes != 1 || project.reruns != 0 || project.mergeAttempts != 0 {
				t.Fatalf("trains %+v (%v), PRs %d, pushes %d, retries %d, merges %d: want no compute for withdrawn riders", kept, err, project.created, project.trainPushes, project.reruns, project.mergeAttempts)
			}
			for _, car := range kept[0].Cars {
				if car.State != train.CarReturned {
					t.Fatalf("car %+v: want it returned without blame", car)
				}
			}
		})
	}
}
