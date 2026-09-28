package lifecycle

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
)

func lifecycleFixture(t *testing.T) (Service, state.TaskMeta) {
	t.Helper()
	directory := t.TempDir()
	meta := state.TaskMeta{ID: "task", SpawnGen: "generation-1", Backend: "native", Worktree: filepath.Join(directory, "worktree"), TaskTmp: filepath.Join(directory, "scratch")}
	if err := os.MkdirAll(meta.TaskTmp, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteTaskMeta(directory, meta); err != nil {
		t.Fatal(err)
	}
	return Service{StateDir: directory, PauseWait: 10 * time.Millisecond, Operations: Operations{
		Prepare: func(context.Context, state.TaskMeta, string) error { return nil },
		Stop:    func(context.Context, state.TaskMeta) ([]string, error) { return []string{"fixture process"}, nil },
		Resume:  func(context.Context, state.TaskMeta, state.Lifecycle) error { return nil },
		Archive: func(context.Context, state.TaskMeta, *state.Lifecycle) (Preservation, error) {
			return Preservation{Kept: []string{"worktree"}}, nil
		},
		Memory: func() (uint64, error) { return 5 << 30, nil },
		Notify: func(state.Lifecycle) error { return nil },
	}}, meta
}

func TestPauseReleasesResourcesAfterTheStoppingPointDeadline(t *testing.T) {
	service, meta := lifecycleFixture(t)
	var phases []string
	var waitingAt, stoppedAt time.Time
	service.Operations.Prepare = func(ctx context.Context, _ state.TaskMeta, _ string) error {
		waitingAt = time.Now()
		<-ctx.Done()
		return ctx.Err()
	}
	service.Operations.Stop = func(_ context.Context, _ state.TaskMeta) ([]string, error) {
		stoppedAt = time.Now()
		record, err := state.ReadLifecycle(service.StateDir, meta.ID)
		if err != nil {
			return nil, err
		}
		phases = append(phases, record.Phase)
		return []string{"server pid 42", "browser pid 43"}, nil
	}
	record, err := service.Run(context.Background(), Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "pause-1", Action: "pause"})
	if err != nil {
		t.Fatal(err)
	}
	if stoppedAt.Sub(waitingAt) > time.Second || record.Phase != "paused" || record.HandoffSaved || len(record.Stopped) != 2 || len(record.Problems) == 0 {
		t.Fatalf("pause did not finish honestly within its bound: %+v", record)
	}
	if !reflect.DeepEqual(phases, []string{"pausing"}) {
		t.Fatalf("state before stop = %v", phases)
	}
	if _, err := state.ReadTaskMeta(service.StateDir, meta.ID); err != nil {
		t.Fatalf("paused task was archived: %v", err)
	}
}

func TestPauseRecordsAHandoffAndAnIdempotentCompletion(t *testing.T) {
	service, meta := lifecycleFixture(t)
	service.Operations.Prepare = func(_ context.Context, _ state.TaskMeta, path string) error {
		return os.WriteFile(path, []byte("Continue the retained branch."), 0o600)
	}
	notices := 0
	service.Operations.Notify = func(state.Lifecycle) error { notices++; return nil }
	request := Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "pause-1", Action: "pause"}
	record, err := service.Run(context.Background(), request)
	if err != nil || !record.HandoffSaved {
		t.Fatalf("handoff not saved: %+v %v", record, err)
	}
	// A fresh value represents a service restarted after the request completed.
	restarted := Service{StateDir: service.StateDir, PauseWait: service.PauseWait, Operations: service.Operations}
	again, err := restarted.Run(context.Background(), request)
	if err != nil || again.Phase != "paused" || notices != 1 {
		t.Fatalf("retry = %+v, %v, notices=%d", again, err, notices)
	}
}

func TestResumeRequiresFiveGigabytesAndKeepsPauseOnRefusal(t *testing.T) {
	service, meta := lifecycleFixture(t)
	if _, err := service.Run(context.Background(), Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "pause-1", Action: "pause"}); err != nil {
		t.Fatal(err)
	}
	resumed := false
	service.Operations.Memory = func() (uint64, error) { return (5 << 30) - 1, nil }
	service.Operations.Resume = func(context.Context, state.TaskMeta, state.Lifecycle) error { resumed = true; return nil }
	_, err := service.Run(context.Background(), Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "resume-1", Action: "resume"})
	if err == nil || resumed {
		t.Fatal("resume ran under its memory floor")
	}
	record, err := state.ReadLifecycle(service.StateDir, meta.ID)
	if err != nil || record.Phase != "paused" {
		t.Fatalf("memory refusal lost pause: %+v %v", record, err)
	}
	service.Operations.Memory = func() (uint64, error) { return 5 << 30, nil }
	record, err = service.Run(context.Background(), Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "resume-2", Action: "resume"})
	if err != nil || record.Phase != "running" || !resumed {
		t.Fatalf("resume = %+v %v", record, err)
	}
}

func TestLifecycleRejectsStaleGenerationBeforeStoppingAnything(t *testing.T) {
	service, meta := lifecycleFixture(t)
	stopped := false
	service.Operations.Stop = func(context.Context, state.TaskMeta) ([]string, error) { stopped = true; return nil, nil }
	_, err := service.Run(context.Background(), Request{ID: meta.ID, Generation: "older", Operation: "stop-1", Action: "stop"})
	if err == nil || stopped {
		t.Fatal("stale card stopped a replacement session")
	}
}

func TestStopNeverArchivesWhileProcessesCouldNotBeStopped(t *testing.T) {
	service, meta := lifecycleFixture(t)
	archived := false
	service.Operations.Stop = func(context.Context, state.TaskMeta) ([]string, error) {
		return nil, errors.New("server pid 42 could not be stopped")
	}
	service.Operations.Archive = func(context.Context, state.TaskMeta, *state.Lifecycle) (Preservation, error) {
		archived = true
		return Preservation{}, nil
	}
	record, err := service.Run(context.Background(), Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "stop-1", Action: "stop"})
	if err == nil || archived || record.Phase != "failed" {
		t.Fatalf("stop = %+v %v, archived=%v", record, err, archived)
	}
}

func TestResumeRecoversAnInterruptedController(t *testing.T) {
	service, meta := lifecycleFixture(t)
	if err := state.WriteLifecycle(service.StateDir, state.Lifecycle{ID: meta.ID, Generation: meta.SpawnGen, RequestGeneration: meta.SpawnGen, Operation: "resume-1", Action: "resume", Phase: "resuming", Session: "original-session"}); err != nil {
		t.Fatal(err)
	}
	resumes := 0
	service.Operations.Resume = func(_ context.Context, _ state.TaskMeta, prior state.Lifecycle) error {
		resumes++
		if prior.Session != "original-session" {
			t.Fatal("interrupted Resume lost its saved session")
		}
		return nil
	}
	request := Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "resume-1", Action: "resume"}
	for attempt := 0; attempt < 2; attempt++ {
		result, err := service.Run(context.Background(), request)
		if err != nil || result.Phase != "running" {
			t.Fatalf("interrupted resume=%+v %v", result, err)
		}
	}
	if resumes != 1 {
		t.Fatalf("launched %d times", resumes)
	}
}

func TestResumeRetryRecognizesItsPublishedReplacementGeneration(t *testing.T) {
	service, meta := lifecycleFixture(t)
	request := Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "resume-1", Action: "resume"}
	if err := state.WriteLifecycle(service.StateDir, state.Lifecycle{ID: meta.ID, Generation: meta.SpawnGen, RequestGeneration: meta.SpawnGen, Operation: request.Operation, Action: "resume", Phase: "resuming", Session: "retained-session"}); err != nil {
		t.Fatal(err)
	}
	meta.SpawnGen, meta.ResumeOperation = "replacement-generation", request.Operation
	if err := state.WriteTaskMeta(service.StateDir, meta); err != nil {
		t.Fatal(err)
	}
	service.Operations.Resume = func(_ context.Context, got state.TaskMeta, prior state.Lifecycle) error {
		if got.SpawnGen != meta.SpawnGen || prior.Session != "retained-session" {
			t.Fatalf("lost interrupted launch identity: %+v %+v", got, prior)
		}
		return nil
	}
	record, err := service.Run(t.Context(), request)
	if err != nil || record.Phase != "running" || record.Generation != meta.SpawnGen {
		t.Fatalf("replacement retry=%+v %v", record, err)
	}
}

func TestResumeRetryCompletesAnExistingLaunchBelowTheMemoryThreshold(t *testing.T) {
	for _, isRunning := range []bool{false, true} {
		t.Run(map[bool]string{false: "requires a new launch", true: "already launched"}[isRunning], func(t *testing.T) {
			service, meta := lifecycleFixture(t)
			request := Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "resume-1", Action: "resume"}
			if err := state.WriteLifecycle(service.StateDir, state.Lifecycle{ID: meta.ID, Generation: meta.SpawnGen, RequestGeneration: meta.SpawnGen, Operation: request.Operation, Action: "resume", Phase: "resuming", Session: "retained-session"}); err != nil {
				t.Fatal(err)
			}
			meta.SpawnGen, meta.ResumeOperation = "replacement-generation", request.Operation
			if err := state.WriteTaskMeta(service.StateDir, meta); err != nil {
				t.Fatal(err)
			}
			service.Operations.IsRunning = func(context.Context, state.TaskMeta) (bool, error) { return isRunning, nil }
			service.Operations.Memory = func() (uint64, error) { return 4 << 30, nil }
			service.Operations.Resume = func(context.Context, state.TaskMeta, state.Lifecycle) error {
				t.Fatal("retry launched another harness below the memory threshold")
				return nil
			}
			record, err := service.Run(t.Context(), request)
			if isRunning && (err != nil || record.Phase != "running" || record.Generation != meta.SpawnGen) {
				t.Fatalf("existing launch was not recovered: %+v %v", record, err)
			}
			if !isRunning && err == nil {
				t.Fatal("new launch bypassed the memory threshold")
			}
		})
	}
}
