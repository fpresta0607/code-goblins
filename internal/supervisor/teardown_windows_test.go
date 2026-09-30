package supervisor

import (
	"os"
	"slices"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestBoardKeepsWindowsTeardownAfterResumeAndRestart(t *testing.T) {
	started, exists := proc.StartTime(os.Getpid())
	if !exists {
		t.Fatal("could not identify the test process")
	}
	for _, phase := range []string{"paused", "running", "stopped"} {
		t.Run(phase, func(t *testing.T) {
			handler, h := orderBoard(t)
			meta := state.TaskMeta{ID: "task", Title: "Retained task", SpawnGen: "generation-1", Project: h.Root, Worktree: h.Root, Backend: "native"}
			if err := state.WriteTaskMeta(h.State, meta); err != nil {
				t.Fatal(err)
			}
			record := state.Lifecycle{ID: meta.ID, Generation: meta.SpawnGen, Operation: "action-1", Action: "resume", Phase: phase, Updated: time.Now(), Teardown: []state.TeardownProcess{{PID: os.Getpid(), Started: started, Name: "chrome.exe"}}}
			if err := state.WriteLifecycle(h.State, record); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				snapshot, err := handler.Service.Snapshot()
				if err != nil {
					t.Fatal(err)
				}
				index := slices.IndexFunc(snapshot.Tasks, func(task Task) bool { return task.ID == meta.ID })
				if index < 0 || snapshot.Tasks[index].Lifecycle == nil || len(snapshot.Tasks[index].Lifecycle.Teardown) != 1 {
					t.Fatalf("board hid teardown in %s: %+v", phase, snapshot.Tasks)
				}
				store, err := Open(h)
				if err != nil {
					t.Fatal(err)
				}
				handler.Service.Store = store
			}
			record.Teardown[0].Started = started.Add(-time.Hour)
			if err := state.WriteLifecycle(h.State, record); err != nil {
				t.Fatal(err)
			}
			snapshot, err := handler.Service.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			for _, task := range snapshot.Tasks {
				if task.Lifecycle != nil && len(task.Lifecycle.Teardown) != 0 {
					t.Fatalf("board retained a replaced identity: %+v", task)
				}
			}
		})
	}
}
