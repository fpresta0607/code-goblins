package supervisor

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleet"
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
	if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: meta.ID, Generation: meta.SpawnGen, Operation: "pause-1", Action: "pause", Phase: "paused", GateRun: "run-1", Updated: time.Now()}); err != nil {
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
				if task.Phase != "paused" || task.Lifecycle == nil || !task.Lifecycle.ValidationRestarts {
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
