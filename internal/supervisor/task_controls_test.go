package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/lifecycle"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

func taskControlRequest(handler *HTTP, path string, input map[string]string) *httptest.ResponseRecorder {
	data, _ := json.Marshal(input)
	request := httptest.NewRequest("POST", "http://board.local"+path, strings.NewReader(string(data)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://board.local")
	request.Header.Set("X-CFO-Token", orderToken)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestQueuedAdjustmentSavesOrSendsANoteWithoutChangingTheTask(t *testing.T) {
	for _, action := range []string{"save", "note"} {
		t.Run(action, func(t *testing.T) {
			handler, h := orderBoard(t)
			writeFile(t, filepath.Join(h.Data, "backlog.md"), "## Queued\n- **task** - Original title (repo: project)\n  Detail.\n")
			before, _ := fleet.ReadQueuedTask(h, "task")
			input := map[string]string{"task": "task", "revision": before.Revision, "operation": "edit-1", "text": "New title\nNew detail.", "action": action}
			response := taskControlRequest(handler, "/api/tasks/adjust", input)
			if response.Code != 200 {
				t.Fatalf("adjust=%d %s", response.Code, response.Body)
			}
			after, _ := fleet.ReadQueuedTask(h, "task")
			var receipt struct {
				Revision string `json:"revision"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &receipt); err != nil || receipt.Revision != after.Revision {
				t.Fatalf("adjustment did not return the saved revision: %+v %v", receipt, err)
			}
			pending, _ := wake.Pending(h.State)
			if action == "save" && (after.Row.Title != "New title" || after.Detail != "New detail.") {
				t.Fatalf("saved=%+v", after)
			}
			if action == "note" && (after.Revision != before.Revision || len(pending) != 1 || pending[0].Key != "task") {
				t.Fatalf("note changed task or lost wake: %+v %+v", after, pending)
			}
			input["revision"] = "stale"
			if response := taskControlRequest(handler, "/api/tasks/adjust", input); response.Code != 409 {
				t.Fatalf("stale edit=%d", response.Code)
			}
		})
	}
}

func TestBoardLifecycleCallsTheCLIAndRefusesStaleGeneration(t *testing.T) {
	spawner := &spawnRecorder{}
	handler, h := startBoard(t, 5*gigabyte, spawner)
	meta := state.TaskMeta{ID: "task", Title: "Do work", SpawnGen: "generation-1", Project: h.Root, Worktree: h.Root, Backend: "native"}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	input := map[string]string{"task": "task", "generation": "stale", "action": "pause", "operation": "pause-1"}
	if response := taskControlRequest(handler, "/api/tasks/lifecycle", input); response.Code != 409 {
		t.Fatalf("stale action=%d", response.Code)
	}
	input["generation"] = meta.SpawnGen
	if response := taskControlRequest(handler, "/api/tasks/lifecycle", input); response.Code != 202 {
		t.Fatalf("pause=%d %s", response.Code, response.Body)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		handler.Service.starts.Lock()
		waiting := handler.Service.changing[meta.ID] != ""
		handler.Service.starts.Unlock()
		if !waiting {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	calls := spawner.recorded()
	if len(calls) != 1 || calls[0][0] != "pause" || calls[0][1] != "task" {
		t.Fatalf("CLI calls=%v", calls)
	}
}

func TestQueuedAdjustmentRetryReturnsItsReceiptWithoutRepeatingTheChange(t *testing.T) {
	for _, action := range []string{"save", "note"} {
		t.Run(action, func(t *testing.T) {
			handler, h := orderBoard(t)
			writeFile(t, filepath.Join(h.Data, "backlog.md"), "## Queued\n- **task** - Original title (repo: project)\n  Detail.\n")
			before, _ := fleet.ReadQueuedTask(h, "task")
			input := map[string]string{"task": "task", "revision": before.Revision, "operation": "edit-1", "text": "New title\nNew detail.", "action": action}
			first := taskControlRequest(handler, "/api/tasks/adjust", input)
			if first.Code != 200 {
				t.Fatalf("first request=%d %s", first.Code, first.Body)
			}
			after, _ := fleet.ReadQueuedTask(h, "task")
			if err := fleet.SaveQueuedTask(h, "task", after.Revision, "Later title\nLater detail."); err != nil {
				t.Fatal(err)
			}
			retry := taskControlRequest(handler, "/api/tasks/adjust", input)
			if retry.Code != 200 || retry.Body.String() != first.Body.String() {
				t.Fatalf("lost-response retry=%d %s, want %s", retry.Code, retry.Body, first.Body)
			}
			current, _ := fleet.ReadQueuedTask(h, "task")
			if current.Row.Title != "Later title" {
				t.Fatalf("retry overwrote newer content: %+v", current)
			}
			input["text"] = "Different request"
			if response := taskControlRequest(handler, "/api/tasks/adjust", input); response.Code != 409 {
				t.Fatalf("operation reused with different input=%d", response.Code)
			}
			pending, _ := wake.Pending(h.State)
			if action == "note" && len(pending) != 1 {
				t.Fatalf("retry duplicated note: %+v", pending)
			}
		})
	}
}

func TestPausedTaskSurvivesSnapshotRestartWithoutAnUnavailableState(t *testing.T) {
	handler, h := orderBoard(t)
	meta := state.TaskMeta{ID: "task", Title: "Retained task", SpawnGen: "generation-1", Project: h.Root, Worktree: h.Root, Backend: "native"}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	if _, err := wake.AppendOnce(h.State, "old-failure", "notify", meta.ID, "failed: old launch failure"); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: meta.ID, Generation: meta.SpawnGen, Operation: "pause-1", Action: "pause", Phase: "paused", Reason: "Requested from the board", GateRun: "run-1", Updated: time.Now()}); err != nil {
		t.Fatal(err)
	}
	for turn := 0; turn < 2; turn++ {
		snapshot, err := handler.Service.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, task := range snapshot.Tasks {
			if task.ID == meta.ID {
				found = true
				if task.Phase != "paused" || task.Lifecycle == nil || !task.Lifecycle.ValidationRestarts || task.Activity != "Requested from the board" {
					t.Fatalf("task=%+v", task)
				}
			}
		}
		if !found {
			t.Fatal("paused task disappeared")
		}
		store, err := Open(h)
		if err != nil {
			t.Fatal(err)
		}
		handler.Service.Store = store
	}
}

func TestSuccessfulCLIActionClearsAnEarlierBoardFailure(t *testing.T) {
	for _, hasPrior := range []bool{false, true} {
		name := "no prior lifecycle"
		if hasPrior {
			name = "same operation recovered"
		}
		t.Run(name, func(t *testing.T) {
			spawner := &spawnRecorder{output: "fixture command failed", err: errors.New("exit 1")}
			handler, h := startBoard(t, 5*gigabyte, spawner)
			meta := state.TaskMeta{ID: "task", SpawnGen: "generation-1", Project: h.Root, Worktree: h.Root, Backend: "native"}
			if err := state.WriteTaskMeta(h.State, meta); err != nil {
				t.Fatal(err)
			}
			record := state.Lifecycle{ID: meta.ID, Generation: meta.SpawnGen, Operation: "pause-1", Action: "pause", Phase: "failed", Updated: time.Now().Add(-time.Minute)}
			if hasPrior {
				if err := state.WriteLifecycle(h.State, record); err != nil {
					t.Fatal(err)
				}
			}
			response := taskControlRequest(handler, "/api/tasks/lifecycle", map[string]string{"task": meta.ID, "generation": meta.SpawnGen, "operation": record.Operation, "action": "pause"})
			if response.Code != 202 {
				t.Fatalf("pause=%d %s", response.Code, response.Body)
			}
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				handler.Service.starts.Lock()
				_, isFailed := handler.Service.changeErrors[meta.ID]
				handler.Service.starts.Unlock()
				if isFailed {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			for _, isRecovered := range []bool{false, true} {
				if isRecovered {
					record.Phase, record.Updated = "paused", time.Now()
					if err := state.WriteLifecycle(h.State, record); err != nil {
						t.Fatal(err)
					}
				}
				snapshot, err := handler.Service.Snapshot()
				if err != nil {
					t.Fatal(err)
				}
				index := slices.IndexFunc(snapshot.Tasks, func(task Task) bool { return task.ID == meta.ID })
				if index < 0 {
					t.Fatalf("tasks=%+v", snapshot.Tasks)
				}
				message := snapshot.Tasks[index].ActionError
				if isRecovered && message != "" || !isRecovered && !strings.Contains(message, "fixture command failed") {
					t.Fatalf("recovered=%t error=%q", isRecovered, message)
				}
			}
		})
	}
}

func TestRefusedResumeLeavesTheSharedSnapshotAndItsEarlierFailureUntilOneIsAccepted(t *testing.T) {
	// Arrange
	spawner := &spawnRecorder{release: make(chan struct{})}
	t.Cleanup(func() { close(spawner.release) })
	handler, h := startBoard(t, 8*gigabyte, spawner)
	service := handler.Service
	meta := state.TaskMeta{ID: "task", SpawnGen: "generation-1", Project: h.Root, Worktree: h.Root, Backend: "native"}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: meta.ID, Generation: meta.SpawnGen, RequestGeneration: meta.SpawnGen, Operation: "pause-1", Action: "pause", Phase: "paused", Updated: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	prior, err := state.ReadLifecycle(h.State, meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	service.starts.Lock()
	service.changeErrors = map[string]taskChangeError{meta.ID: {Message: "earlier failure", Generation: meta.SpawnGen, Operation: prior.Operation, Updated: prior.Updated}}
	service.starts.Unlock()
	reading, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	service.Options.Dispatch.Memory = func() (Memory, error) {
		if calls.Add(1) == 1 {
			close(reading)
			<-release
			return Memory{}, errors.New("memory reading failed")
		}
		return Memory{Available: 8 * gigabyte, CommitAvailable: 8 * gigabyte}, nil
	}
	input := map[string]string{"task": meta.ID, "generation": meta.SpawnGen, "operation": "resume-1", "action": "resume"}
	refused := make(chan int, 1)
	go func() { refused <- taskControlRequest(handler, "/api/tasks/lifecycle", input).Code }()
	<-reading
	during, err := service.SnapshotSince(service.Revision())
	close(release)
	if err != nil {
		t.Fatal(err)
	}
	if index := slices.IndexFunc(during.Tasks, func(task Task) bool { return task.ID == meta.ID }); index < 0 || during.Tasks[index].Phase != "resuming" || during.Tasks[index].ActionError != "earlier failure" {
		t.Fatalf("the snapshot built while Resume read memory: %+v", during.Tasks)
	}
	if code := <-refused; code != 409 {
		t.Fatalf("resume with an unreadable memory=%d", code)
	}

	// Act
	after, err := service.SnapshotSince(service.Revision())

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if index := slices.IndexFunc(after.Tasks, func(task Task) bool { return task.ID == meta.ID }); index < 0 || after.Tasks[index].Phase == "resuming" || after.Tasks[index].ActionError != "earlier failure" {
		t.Fatalf("after a refused Resume the shared snapshot shows: %+v", after.Tasks)
	}
	input["operation"] = "resume-2"
	if response := taskControlRequest(handler, "/api/tasks/lifecycle", input); response.Code != 202 {
		t.Fatalf("resume=%d %s", response.Code, response.Body)
	}
	accepted, err := service.SnapshotSince(service.Revision())
	if err != nil {
		t.Fatal(err)
	}
	if index := slices.IndexFunc(accepted.Tasks, func(task Task) bool { return task.ID == meta.ID }); index < 0 || accepted.Tasks[index].Phase != "resuming" || accepted.Tasks[index].ActionError != "" {
		t.Fatalf("an accepted Resume does not clear the earlier failure: %+v", accepted.Tasks)
	}
}

func TestBoardFailureFollowsItsOwnResumeButNotALaterSuccessfulAction(t *testing.T) {
	for _, isLaterAction := range []bool{false, true} {
		name := "failed replacement generation"
		if isLaterAction {
			name = "later action in same generation"
		}
		t.Run(name, func(t *testing.T) {
			handler, h := startBoard(t, 5*gigabyte, &spawnRecorder{})
			meta := state.TaskMeta{ID: "task", SpawnGen: "original", Backend: "native"}
			if err := state.WriteTaskMeta(h.State, meta); err != nil {
				t.Fatal(err)
			}
			prior := state.Lifecycle{ID: meta.ID, Generation: meta.SpawnGen, RequestGeneration: meta.SpawnGen, Operation: "pause-1", Action: "pause", Phase: "paused", Updated: time.Now().Add(-time.Minute)}
			if err := state.WriteLifecycle(h.State, prior); err != nil {
				t.Fatal(err)
			}
			handler.Service.Options.Dispatch.Spawn = func(context.Context, []string) (string, error) {
				record := prior
				record.Action, record.Phase, record.Operation, record.Generation = "resume", "failed", "resume-1", "replacement"
				if isLaterAction {
					record.Action, record.Phase, record.Operation, record.Generation = "pause", "paused", "external-pause", meta.SpawnGen
				}
				record.Updated = time.Now()
				meta.SpawnGen = record.Generation
				if err := state.WriteTaskMeta(h.State, meta); err != nil {
					return "", err
				}
				if err := state.WriteLifecycle(h.State, record); err != nil {
					return "", err
				}
				return "fixture command failed", errors.New("exit 1")
			}
			response := taskControlRequest(handler, "/api/tasks/lifecycle", map[string]string{"task": meta.ID, "generation": meta.SpawnGen, "operation": "resume-1", "action": "resume"})
			if response.Code != 202 {
				t.Fatalf("resume=%d %s", response.Code, response.Body)
			}
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				handler.Service.starts.Lock()
				_, isFailed := handler.Service.changeErrors["task"]
				handler.Service.starts.Unlock()
				if isFailed {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			snapshot, err := handler.Service.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			index := slices.IndexFunc(snapshot.Tasks, func(task Task) bool { return task.ID == "task" })
			if index < 0 {
				t.Fatalf("tasks=%+v", snapshot.Tasks)
			}
			message := snapshot.Tasks[index].ActionError
			if isLaterAction && message != "" || !isLaterAction && message != "fixture command failed" {
				t.Fatalf("later action=%t error=%q", isLaterAction, message)
			}
		})
	}
}

func TestQueuedStopShowsFailureAfterWritingItsLifecycle(t *testing.T) {
	handler, h := startBoard(t, 5*gigabyte, &spawnRecorder{})
	writeFile(t, filepath.Join(h.Data, "backlog.md"), "## Queued\n- **queued-task** - Retire this task (repo: project)\n")
	queued, err := fleet.ReadQueuedTask(h, "queued-task")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lock.AcquireExclusiveNamed(h.State, ".backlog.lock"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.ReleaseExclusiveNamed(h.State, ".backlog.lock") })
	handler.Service.Options.Dispatch.Spawn = func(context.Context, []string) (string, error) {
		_, err := lifecycle.StopQueued(h, lifecycle.Request{ID: queued.Row.ID, Operation: "stop-1", Action: "stop"}, queued.Revision)
		return "", err
	}
	response := taskControlRequest(handler, "/api/tasks/lifecycle", map[string]string{"task": queued.Row.ID, "revision": queued.Revision, "operation": "stop-1", "action": "stop"})
	if response.Code != 202 {
		t.Fatalf("stop=%d %s", response.Code, response.Body)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		handler.Service.starts.Lock()
		_, isFailed := handler.Service.changeErrors[queued.Row.ID]
		handler.Service.starts.Unlock()
		if isFailed {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	snapshot, err := handler.Service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	index := slices.IndexFunc(snapshot.Tasks, func(task Task) bool { return task.ID == queued.Row.ID })
	if index < 0 || snapshot.Tasks[index].ActionError == "" || snapshot.Tasks[index].Phase != "queued" {
		t.Fatalf("failed Stop is not visible: %+v", snapshot.Tasks)
	}
	if history := finishedTasks(h, time.Now()); slices.ContainsFunc(history, func(task Task) bool { return task.ID == "finished:"+queued.Row.ID }) {
		t.Fatalf("failed Stop shows the queued task as Completed: %+v", history)
	}
}

func TestBoardResumeLetsTheCLIReconcileOnlyAnOperationBoundLaunchBelowFiveGigabytes(t *testing.T) {
	for _, test := range []struct {
		phase, action, resumeOperation string
		status                         int
	}{
		{phase: "paused", action: "pause", status: 409},
		{phase: "resuming", action: "resume", status: 409},
		{phase: "resuming", action: "resume", resumeOperation: "prior-resume", status: 202},
		{phase: "failed", action: "resume", resumeOperation: "prior-resume", status: 202},
	} {
		t.Run(test.phase+"/"+test.resumeOperation, func(t *testing.T) {
			spawner := &spawnRecorder{}
			handler, h := startBoard(t, 4*gigabyte, spawner)
			meta := state.TaskMeta{ID: "task", SpawnGen: "generation-2", ResumeOperation: test.resumeOperation, Backend: "native"}
			if err := state.WriteTaskMeta(h.State, meta); err != nil {
				t.Fatal(err)
			}
			if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: meta.ID, Generation: meta.SpawnGen, Operation: "prior-resume", Action: test.action, Phase: test.phase}); err != nil {
				t.Fatal(err)
			}
			response := taskControlRequest(handler, "/api/tasks/lifecycle", map[string]string{"task": meta.ID, "generation": meta.SpawnGen, "operation": "retry-resume", "action": "resume"})
			if response.Code != test.status {
				t.Fatalf("resume=%d %s, want %d", response.Code, response.Body, test.status)
			}
		})
	}
}

func TestBoardResumeNeedsFiveGigabytesOfBothMemoryAndCommitAndNamesWhatIsShort(t *testing.T) {
	tests := []struct {
		name              string
		available, commit uint64
		status            int
		want              string
	}{
		{name: "memory short", available: 4 * gigabyte, commit: 40 * gigabyte, status: 409, want: "Only 4.0 GB of memory is free; Resume needs 5 GB to keep the 4 GB floor"},
		{name: "commit short", available: 16 * gigabyte, commit: 2*gigabyte + gigabyte/2, status: 409, want: "Only 2.5 GB of commit (RAM plus page file) is free; Resume needs 5 GB to keep the 4 GB floor"},
		{name: "both fine", available: 5 * gigabyte, commit: 5 * gigabyte, status: 202},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			spawner := &spawnRecorder{}
			handler, h := startBoardWith(t, Memory{Available: test.available, Total: 32 * gigabyte, CommitAvailable: test.commit, CommitLimit: 48 * gigabyte}, spawner)
			meta := state.TaskMeta{ID: "task", SpawnGen: "generation-1", Backend: "native"}
			if err := state.WriteTaskMeta(h.State, meta); err != nil {
				t.Fatal(err)
			}
			if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: meta.ID, Generation: meta.SpawnGen, Operation: "pause-1", Action: "pause", Phase: "paused"}); err != nil {
				t.Fatal(err)
			}

			// Act
			response := taskControlRequest(handler, "/api/tasks/lifecycle", map[string]string{"task": meta.ID, "generation": meta.SpawnGen, "operation": "resume-1", "action": "resume"})

			// Assert
			if response.Code != test.status || !strings.Contains(response.Body.String(), test.want) {
				t.Fatalf("resume = %d %s, want %d saying %q", response.Code, response.Body, test.status, test.want)
			}
		})
	}
}

func TestInterruptedResumeRemainsAvailableAfterPublishingAReplacementGeneration(t *testing.T) {
	handler, h := orderBoard(t)
	meta := state.TaskMeta{ID: "task", SpawnGen: "replacement", ResumeOperation: "resume-1", Backend: "native"}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: meta.ID, Generation: "original", Operation: meta.ResumeOperation, Action: "resume", Phase: "resuming"}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := handler.Service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range snapshot.Tasks {
		if task.ID == meta.ID && task.Lifecycle != nil && task.Lifecycle.Action == "resume" && task.Phase != "resuming" {
			return
		}
	}
	t.Fatalf("interrupted Resume cannot be retried: %+v", snapshot.Tasks)
}

func TestRequeuedTaskRegainsQueueControlsAfterAnEarlierQueuedStop(t *testing.T) {
	handler, h := startBoard(t, 5*gigabyte, &spawnRecorder{})
	backlog := "## Queued\n- **queued-task** - Retire this task (repo: project)\n"
	writeFile(t, filepath.Join(h.Data, "backlog.md"), backlog)
	writeFile(t, filepath.Join(h.Data, "queued-task", "brief.md"), "# Brief queued-task\n\n## Task\n\nRetire this task.\n")
	queued, err := fleet.ReadQueuedTask(h, "queued-task")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lifecycle.StopQueued(h, lifecycle.Request{ID: queued.Row.ID, Operation: "stop-1", Action: "stop"}, queued.Revision); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(h.Data, "backlog.md"), backlog)
	snapshot, err := handler.Service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	index := slices.IndexFunc(snapshot.Tasks, func(task Task) bool { return task.ID == queued.Row.ID })
	if index < 0 {
		t.Fatalf("requeued task is missing: %+v", snapshot.Tasks)
	}
	if task := snapshot.Tasks[index]; task.Phase != "queued" || task.Archived || task.Lifecycle != nil || task.QueueRevision == "" || !task.Brief {
		t.Fatalf("requeued task lost its queue controls or brief: %+v", task)
	}
}

func TestStoppedCardNeverPromisesAValidationRestart(t *testing.T) {
	handler, h := orderBoard(t)
	meta := state.TaskMeta{ID: "task", Title: "Gated task", SpawnGen: "generation-1", Project: h.Root, Worktree: h.Root, TaskTmp: filepath.Join(h.State, "tasktmp", "task"), Backend: "native"}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	service := lifecycle.Service{StateDir: h.State, PauseWait: 10 * time.Millisecond, Operations: lifecycle.Operations{
		Prepare: func(context.Context, state.TaskMeta, string) error { return nil },
		Stop:    func(context.Context, state.TaskMeta, *state.Lifecycle) ([]string, error) { return nil, nil },
		Checkpoint: func(_ context.Context, _ state.TaskMeta, record *state.Lifecycle) error {
			if record.Action == "pause" {
				record.GateRun, record.GateIntent, record.GateHead = "run-1", "saved intent", strings.Repeat("a", 40)
			}
			return nil
		},
		Archive: func(context.Context, state.TaskMeta, *state.Lifecycle) (lifecycle.Preservation, error) {
			return lifecycle.Preservation{Kept: []string{"branch gb-task"}}, nil
		},
		Notify: func(state.Lifecycle) error { return nil },
	}}
	lifecycleCard := func(id string) *LifecycleStatus {
		t.Helper()
		snapshot, err := handler.Service.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		index := slices.IndexFunc(snapshot.Tasks, func(task Task) bool { return task.ID == id })
		if index < 0 || snapshot.Tasks[index].Lifecycle == nil {
			t.Fatalf("no lifecycle card for %s: %+v", id, snapshot.Tasks)
		}
		return snapshot.Tasks[index].Lifecycle
	}
	if _, err := service.Run(t.Context(), lifecycle.Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "pause-1", Action: "pause"}); err != nil {
		t.Fatal(err)
	}
	if !lifecycleCard(meta.ID).ValidationRestarts {
		t.Fatal("paused card with an interrupted gate does not say validation restarts")
	}
	stopped, err := service.Run(t.Context(), lifecycle.Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "stop-1", Action: "stop"})
	if err != nil || stopped.GateRun != "run-1" {
		t.Fatalf("stop lost gate custody: %+v %v", stopped, err)
	}
	if lifecycleCard(meta.ID).ValidationRestarts {
		t.Fatal("stopped card promises a validation restart")
	}
	if err := os.Remove(filepath.Join(h.State, meta.ID+".meta")); err != nil {
		t.Fatal(err)
	}
	history := finishedTasks(h, time.Now())
	index := slices.IndexFunc(history, func(task Task) bool { return task.ID == "finished:"+meta.ID })
	if index < 0 || history[index].Lifecycle == nil || history[index].Lifecycle.ValidationRestarts {
		t.Fatalf("archived stopped card promises a validation restart: %+v", history)
	}
}

func TestValidationRestartIsPromisedOnlyWhenResumeCanFollow(t *testing.T) {
	for _, test := range []struct {
		action, phase string
		want          bool
	}{
		{"pause", "paused", true},
		{"resume", "resuming", true},
		{"resume", "failed", true},
		{"pause", "failed", false},
		{"stop", "stopping", false},
		{"stop", "stopped", false},
		{"stop", "failed", false},
	} {
		t.Run(test.action+" "+test.phase, func(t *testing.T) {
			handler, h := orderBoard(t)
			meta := state.TaskMeta{ID: "task", Title: "Gated task", SpawnGen: "generation-1", Project: h.Root, Worktree: h.Root, TaskTmp: filepath.Join(h.State, "tasktmp", "task"), Backend: "native"}
			if err := state.WriteTaskMeta(h.State, meta); err != nil {
				t.Fatal(err)
			}
			if test.action == "pause" && test.phase == "failed" {
				service := lifecycle.Service{StateDir: h.State, PauseWait: 10 * time.Millisecond, Operations: lifecycle.Operations{
					Prepare: func(context.Context, state.TaskMeta, string) error { return nil },
					Stop:    func(context.Context, state.TaskMeta, *state.Lifecycle) ([]string, error) { return nil, nil },
					Checkpoint: func(_ context.Context, _ state.TaskMeta, record *state.Lifecycle) error {
						record.GateRun, record.GateIntent, record.GateHead = "run-1", "saved intent", strings.Repeat("a", 40)
						return errors.New("gate commits are pinned locally but could not merge into the task branch")
					},
					Notify: func(state.Lifecycle) error { return nil },
				}}
				record, err := service.Run(t.Context(), lifecycle.Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "pause-1", Action: "pause"})
				if err == nil || record.Phase != "failed" || record.GateRun != "run-1" || len(record.Problems) == 0 {
					t.Fatalf("failed pause lost its gate custody or diagnostic: %+v %v", record, err)
				}
			} else if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: meta.ID, Generation: meta.SpawnGen, Operation: "operation-1", Action: test.action, Phase: test.phase, GateRun: "run-1", GateIntent: "saved intent", Updated: time.Now()}); err != nil {
				t.Fatal(err)
			}
			snapshot, err := handler.Service.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			index := slices.IndexFunc(snapshot.Tasks, func(task Task) bool { return task.ID == meta.ID })
			if index < 0 || snapshot.Tasks[index].Lifecycle == nil {
				t.Fatalf("no lifecycle card: %+v", snapshot.Tasks)
			}
			if got := snapshot.Tasks[index].Lifecycle.ValidationRestarts; got != test.want {
				t.Fatalf("validation restarts=%v, want %v", got, test.want)
			}
		})
	}
}
