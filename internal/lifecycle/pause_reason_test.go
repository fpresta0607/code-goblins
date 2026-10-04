package lifecycle

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestPauseRejectsMissingOrUnknownReasonBeforeStopping(t *testing.T) {
	for _, reason := range []string{"", "Requested by the operator", "unknown"} {
		t.Run(reason, func(t *testing.T) {
			service, meta := lifecycleFixture(t)
			isStopped := false
			service.Operations.Stop = func(context.Context, state.TaskMeta, *state.Lifecycle) ([]string, error) {
				isStopped = true
				return nil, nil
			}

			_, err := service.Run(t.Context(), Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "pause-reason", Action: "pause", Reason: reason})

			if err == nil || isStopped {
				t.Fatalf("pause reason %q: error=%v stopped=%v", reason, err, isStopped)
			}
		})
	}
}

func TestPauseOperationCannotChangeItsConditionOnRetry(t *testing.T) {
	service, meta := lifecycleFixture(t)
	request := Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "pause-reason", Action: "pause", Reason: "overlord"}
	if _, err := service.Run(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	request.Reason = "allowance"
	request.Until = time.Now().Add(time.Hour).UTC().Format(time.RFC3339)

	if _, err := service.Run(t.Context(), request); err == nil {
		t.Fatal("retry replaced the Overlord's manual pause")
	}
}

func TestResumeAdmissionFailureLeavesThePausedTaskIntact(t *testing.T) {
	service, meta := lifecycleFixture(t)
	if _, err := service.Run(t.Context(), Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "pause-reason", Action: "pause", Reason: "overlord"}); err != nil {
		t.Fatal(err)
	}
	isResumed := false
	service.Operations.Admit = func() error { return errors.New("live cap reached") }
	service.Operations.Resume = func(context.Context, state.TaskMeta, state.Lifecycle) error {
		isResumed = true
		return nil
	}

	_, err := service.Run(t.Context(), Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "resume-reason", Action: "resume"})

	record, readErr := state.ReadLifecycle(service.StateDir, meta.ID)
	if err == nil || readErr != nil || isResumed || record.Phase != "paused" {
		t.Fatalf("admission lost paused state: %+v, %v, %v, resumed=%v", record, err, readErr, isResumed)
	}
}

func TestPauseCannotDependOnItsOwnTaskFinishing(t *testing.T) {
	service, meta := lifecycleFixture(t)

	_, err := service.Run(t.Context(), Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "pause-self", Action: "pause", Reason: "dependency", Until: "task:" + meta.ID})

	if err == nil {
		t.Fatal("accepted a condition this paused task cannot clear")
	}
}
