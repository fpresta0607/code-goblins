package lifecycle

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/crewstate"
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
	return Service{StateDir: directory, PrepareWait: 10 * time.Millisecond, PauseWait: 10 * time.Millisecond, Operations: Operations{
		Prepare: func(context.Context, state.TaskMeta, string) error { return nil },
		Stop: func(context.Context, state.TaskMeta, *state.Lifecycle) ([]string, error) {
			return []string{"fixture process"}, nil
		},
		Resume: func(context.Context, state.TaskMeta, state.Lifecycle) error { return nil },
		Archive: func(context.Context, state.TaskMeta, *state.Lifecycle) (Preservation, error) {
			return Preservation{Kept: []string{"worktree"}}, nil
		},
		Memory: func() (uint64, uint64, error) { return 5 << 30, 5 << 30, nil },
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
	service.Operations.Stop = func(_ context.Context, _ state.TaskMeta, _ *state.Lifecycle) ([]string, error) {
		stoppedAt = time.Now()
		record, err := state.ReadLifecycle(service.StateDir, meta.ID)
		if err != nil {
			return nil, err
		}
		phases = append(phases, record.Phase)
		return []string{"server pid 42", "browser pid 43"}, nil
	}
	record, err := service.Run(context.Background(), Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "pause-1", Action: "pause", Reason: "overlord"})
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
	request := Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "pause-1", Action: "pause", Reason: "overlord"}
	record, err := service.Run(context.Background(), request)
	if err != nil || !record.HandoffSaved {
		t.Fatalf("handoff not saved: %+v %v", record, err)
	}
	// A fresh value represents a service restarted after the request completed.
	restarted := Service{StateDir: service.StateDir, PrepareWait: service.PrepareWait, PauseWait: service.PauseWait, Operations: service.Operations}
	again, err := restarted.Run(context.Background(), request)
	if err != nil || again.Phase != "paused" || notices != 1 {
		t.Fatalf("retry = %+v, %v, notices=%d", again, err, notices)
	}
}

func TestPauseChecksThePublishedHandoffAfterUnconfirmedDelivery(t *testing.T) {
	for _, testCase := range []struct {
		name           string
		publish        func(string) error
		isHandoffSaved bool
	}{
		{name: "published", publish: func(path string) error {
			if err := os.WriteFile(path+".partial", []byte("Continue the retained branch."), 0o600); err != nil {
				return err
			}
			return os.Rename(path+".partial", path)
		}, isHandoffSaved: true},
		{name: "empty", publish: func(path string) error { return os.WriteFile(path, nil, 0o600) }},
		{name: "partial", publish: func(path string) error { return os.WriteFile(path+".partial", []byte("Still writing."), 0o600) }},
		{name: "directory", publish: func(path string) error { return os.Mkdir(path, 0o700) }},
		{name: "absent", publish: func(string) error { return nil }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			service, meta := lifecycleFixture(t)
			retained := filepath.Join(meta.TaskTmp, "handoff.md")
			if err := os.WriteFile(retained, []byte("Earlier handoff."), 0o600); err != nil {
				t.Fatal(err)
			}
			service.Operations.Prepare = func(_ context.Context, _ state.TaskMeta, path string) error {
				if err := testCase.publish(path); err != nil {
					return err
				}
				return context.DeadlineExceeded
			}

			record, err := service.Run(t.Context(), Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "pause-1", Action: "pause", Reason: "overlord"})

			if err != nil || record.Phase != "paused" || record.HandoffSaved != testCase.isHandoffSaved || (len(record.Problems) == 0) != testCase.isHandoffSaved {
				t.Fatalf("published handoff=%v: %+v, %v", testCase.isHandoffSaved, record, err)
			}
			if data, err := os.ReadFile(retained); err != nil || string(data) != "Earlier handoff." {
				t.Fatalf("prior handoff changed: %q, %v", data, err)
			}
		})
	}
}

func TestPauseStartsTheStoppingPointWindowAfterDelivery(t *testing.T) {
	service, meta := lifecycleFixture(t)
	service.PrepareWait = time.Second
	service.PauseWait = 50 * time.Millisecond
	var accepted, stopped time.Time
	service.Operations.Prepare = func(ctx context.Context, _ state.TaskMeta, _ string) error {
		select {
		case <-time.After(100 * time.Millisecond):
			accepted = time.Now()
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	service.Operations.Stop = func(context.Context, state.TaskMeta, *state.Lifecycle) ([]string, error) {
		stopped = time.Now()
		return nil, nil
	}

	record, err := service.Run(t.Context(), Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "pause-1", Action: "pause", Reason: "overlord"})

	if err != nil || accepted.IsZero() || stopped.Sub(accepted) < service.PauseWait || stopped.Sub(accepted) > time.Second || record.Phase != "paused" || record.HandoffSaved || len(record.Problems) == 0 {
		t.Fatalf("stopping-point window after acceptance=%s: %+v, %v", stopped.Sub(accepted), record, err)
	}
}

func TestPublishedHandoffCancelsUnconfirmedDeliveryBeforeStopping(t *testing.T) {
	service, meta := lifecycleFixture(t)
	service.PrepareWait = time.Minute
	prepared := make(chan struct{})
	service.Operations.Prepare = func(ctx context.Context, _ state.TaskMeta, path string) error {
		defer close(prepared)
		if err := os.WriteFile(path, []byte("Continue the retained branch."), 0o600); err != nil {
			return err
		}
		<-ctx.Done()
		return ctx.Err()
	}
	isDeliveryFinished := false
	service.Operations.Stop = func(context.Context, state.TaskMeta, *state.Lifecycle) ([]string, error) {
		select {
		case <-prepared:
			isDeliveryFinished = true
		default:
		}
		return nil, nil
	}
	started := time.Now()

	record, err := service.Run(t.Context(), Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "pause-1", Action: "pause", Reason: "overlord"})

	if err != nil || !record.HandoffSaved || record.Phase != "paused" || !isDeliveryFinished || time.Since(started) > time.Second {
		t.Fatalf("handoff did not finish delivery before stopping: %+v, %v, delivery finished=%v", record, err, isDeliveryFinished)
	}
}

func TestResumeRequiresFiveGigabytesAndKeepsPauseOnRefusal(t *testing.T) {
	service, meta := lifecycleFixture(t)
	if _, err := service.Run(context.Background(), Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "pause-1", Action: "pause", Reason: "overlord"}); err != nil {
		t.Fatal(err)
	}
	resumed := false
	service.Operations.Memory = func() (uint64, uint64, error) { return (5 << 30) - 1, 40 << 30, nil }
	service.Operations.Resume = func(context.Context, state.TaskMeta, state.Lifecycle) error { resumed = true; return nil }
	_, err := service.Run(context.Background(), Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "resume-1", Action: "resume"})
	if err == nil || resumed {
		t.Fatal("resume ran under its memory floor")
	}
	record, err := state.ReadLifecycle(service.StateDir, meta.ID)
	if err != nil || record.Phase != "paused" {
		t.Fatalf("memory refusal lost pause: %+v %v", record, err)
	}
	service.Operations.Memory = func() (uint64, uint64, error) { return 5 << 30, 5 << 30, nil }
	record, err = service.Run(context.Background(), Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "resume-2", Action: "resume"})
	if err != nil || record.Phase != "running" || !resumed {
		t.Fatalf("resume = %+v %v", record, err)
	}
}

func TestResumeNeedsFiveGigabytesOfBothMemoryAndCommitAndNamesWhatIsShort(t *testing.T) {
	tests := []struct {
		name              string
		available, commit uint64
		want, notWant     string
	}{
		{name: "memory short", available: (5 << 30) - 1, commit: 40 << 30, want: "Resume needs at least 5 GB of available memory to keep the 4 GB floor", notWant: "commit"},
		{name: "commit short", available: 16 << 30, commit: (5 << 30) - 1, want: "Resume needs at least 5 GB of commit (RAM plus page file) to keep the 4 GB floor", notWant: "available memory"},
		{name: "both short", available: 3 << 30, commit: 2 << 30, want: "Resume needs at least 5 GB of available memory and of commit (RAM plus page file) to keep the 4 GB floor"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			service, meta := lifecycleFixture(t)
			if _, err := service.Run(context.Background(), Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "pause-1", Action: "pause", Reason: "overlord"}); err != nil {
				t.Fatal(err)
			}
			resumed := false
			service.Operations.Memory = func() (uint64, uint64, error) { return test.available, test.commit, nil }
			service.Operations.Resume = func(context.Context, state.TaskMeta, state.Lifecycle) error { resumed = true; return nil }

			// Act
			_, err := service.Run(context.Background(), Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "resume-1", Action: "resume"})

			// Assert
			if err == nil || resumed || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("resume = %v, resumed %v, want a refusal saying %q", err, resumed, test.want)
			}
			if test.notWant != "" && strings.Contains(err.Error(), test.notWant) {
				t.Fatalf("refusal %q names %q, which is not short", err, test.notWant)
			}
			record, err := state.ReadLifecycle(service.StateDir, meta.ID)
			if err != nil || record.Phase != "paused" {
				t.Fatalf("memory refusal lost pause: %+v %v", record, err)
			}
		})
	}
}

func TestLifecycleRejectsStaleGenerationBeforeStoppingAnything(t *testing.T) {
	service, meta := lifecycleFixture(t)
	stopped := false
	service.Operations.Stop = func(context.Context, state.TaskMeta, *state.Lifecycle) ([]string, error) {
		stopped = true
		return nil, nil
	}
	_, err := service.Run(context.Background(), Request{ID: meta.ID, Generation: "older", Operation: "stop-1", Action: "stop"})
	if err == nil || stopped {
		t.Fatal("stale card stopped a replacement session")
	}
}

func TestStopNeverArchivesWhileProcessesCouldNotBeStopped(t *testing.T) {
	service, meta := lifecycleFixture(t)
	archived := false
	service.Operations.Stop = func(context.Context, state.TaskMeta, *state.Lifecycle) ([]string, error) {
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
			service.Operations.Memory = func() (uint64, uint64, error) { return 4 << 30, 4 << 30, nil }
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

func TestGateRecoverySurvivesAFailedResumeButNotASuccessfulOne(t *testing.T) {
	service, meta := lifecycleFixture(t)
	isGateOpen := true
	service.Operations.Checkpoint = func(_ context.Context, _ state.TaskMeta, record *state.Lifecycle) error {
		if isGateOpen {
			record.GateRun, record.GateIntent, record.GateHead = "run-1", "saved intent", strings.Repeat("a", 40)
		}
		return nil
	}
	var resumedWith []string
	service.Operations.Resume = func(_ context.Context, _ state.TaskMeta, prior state.Lifecycle) error {
		resumedWith = append(resumedWith, prior.GateRun)
		if len(resumedWith) == 1 {
			return errors.New("harness failed to start")
		}
		return nil
	}
	if _, err := service.Run(t.Context(), Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "pause-1", Action: "pause", Reason: "overlord"}); err != nil {
		t.Fatal(err)
	}
	failed, err := service.Run(t.Context(), Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "resume-1", Action: "resume"})
	if err == nil || failed.Phase != "failed" || failed.GateRun != "run-1" {
		t.Fatalf("failed resume lost its interrupted gate: %+v %v", failed, err)
	}
	if _, err := service.Run(t.Context(), Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "resume-2", Action: "resume"}); err != nil {
		t.Fatal(err)
	}
	isGateOpen = false
	paused, err := service.Run(t.Context(), Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "pause-2", Action: "pause", Reason: "overlord"})
	if err != nil || paused.GateRun != "" || paused.GateIntent != "" || paused.GateHead != "" {
		t.Fatalf("pause without an open gate kept stale restart instructions: %+v %v", paused, err)
	}
	if !reflect.DeepEqual(resumedWith, []string{"run-1", "run-1"}) {
		t.Fatalf("resume gates = %v", resumedWith)
	}
}

func TestLifecycleOutcomesNeverHideTheTasksOwnReport(t *testing.T) {
	service, meta := lifecycleFixture(t)
	if err := state.AppendStatus(service.StateDir, meta.ID, "needs-decision: pick a schema"); err != nil {
		t.Fatal(err)
	}
	service.Operations.Resume = func(context.Context, state.TaskMeta, state.Lifecycle) error {
		return errors.New("harness failed to start")
	}
	for _, request := range []Request{{Operation: "pause-1", Action: "pause", Reason: "overlord"}, {Operation: "resume-1", Action: "resume"}} {
		request.ID, request.Generation = meta.ID, meta.SpawnGen
		_, _ = service.Run(t.Context(), request)
		lines, err := state.TailStatus(service.StateDir, meta.ID, 200)
		if err != nil {
			t.Fatal(err)
		}
		if verb, _ := crewstate.LatestVerb(lines); verb != "needs-decision" {
			t.Fatalf("after %s the task's latest report reads %q: %v", request.Action, verb, lines)
		}
	}
	if err := state.AppendStatus(service.StateDir, meta.ID, "failed: build broke"); err != nil {
		t.Fatal(err)
	}
	lines, err := state.TailStatus(service.StateDir, meta.ID, 200)
	if err != nil {
		t.Fatal(err)
	}
	if verb, _ := crewstate.LatestVerb(lines); verb != "failed" {
		t.Fatalf("the task's own failure report is hidden: %q", verb)
	}
}

func TestPauseInstructionPublishesTheHandoffAfterThePush(t *testing.T) {
	handoff := filepath.Join(t.TempDir(), "pause-1.md")
	instruction := PauseInstruction(handoff)
	push := strings.Index(instruction, "push your branch")
	draft := strings.Index(instruction, handoff+".partial")
	publish := strings.LastIndex(instruction, "rename it to "+handoff)
	if push < 0 || draft < push || publish < draft {
		t.Fatalf("handoff is not published last, after the push: %q", instruction)
	}
}
