package supervisor

import (
	"io"
	"os"
	"os/exec"
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
	for _, test := range []struct {
		name        string
		phase       string
		isSameStamp bool
	}{
		{name: "paused", phase: "paused"},
		{name: "running", phase: "running"},
		{name: "stopped", phase: "stopped"},
		{name: "stopped_same_stamp", phase: "stopped", isSameStamp: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			phase := test.phase
			handler, h := orderBoard(t)
			meta := state.TaskMeta{ID: "task", Title: "Retained task", SpawnGen: "generation-1", Project: h.Root, Worktree: h.Root, Backend: "native"}
			if err := state.WriteTaskMeta(h.State, meta); err != nil {
				t.Fatal(err)
			}
			record := state.Lifecycle{ID: meta.ID, Generation: meta.SpawnGen, Operation: "action-1", Action: "resume", Phase: phase, GateRun: "run-1", Updated: time.Now(), Teardown: []state.TeardownProcess{{PID: os.Getpid(), Started: started, Name: "chrome.exe"}}}
			if err := state.WriteLifecycle(h.State, record); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				snapshot, err := handler.Service.Snapshot()
				if err != nil {
					t.Fatal(err)
				}
				index := slices.IndexFunc(snapshot.Tasks, func(task Task) bool { return task.ID == meta.ID })
				if index < 0 || snapshot.Tasks[index].Lifecycle == nil || len(snapshot.Tasks[index].Teardown) != 1 {
					t.Fatalf("board hid teardown in %s: %+v", phase, snapshot.Tasks)
				}
				if phase == "running" && snapshot.Tasks[index].Lifecycle.ValidationRestarts {
					t.Fatal("resumed card promises a validation restart")
				}
				store, err := Open(h)
				if err != nil {
					t.Fatal(err)
				}
				handler.Service.Store = store
			}
			path := state.LifecyclePath(h.State, meta.ID)
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			record.Teardown[0].Started = started.Add(-time.Hour)
			if err := state.WriteLifecycle(h.State, record); err != nil {
				t.Fatal(err)
			}
			if test.isSameStamp {
				if err := os.Chtimes(path, before.ModTime(), before.ModTime()); err != nil {
					t.Fatal(err)
				}
				after, err := os.Stat(path)
				if err != nil || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
					t.Fatalf("did not preserve the lifecycle file signature: %v %v", after, err)
				}
			}
			snapshot, err := handler.Service.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			for _, task := range snapshot.Tasks {
				if len(task.Teardown) != 0 {
					t.Fatalf("board retained a replaced identity: %+v", task)
				}
			}
		})
	}
}

func TestBoardTeardownProcessFixture(t *testing.T) {
	if os.Getenv("CFO_TEARDOWN_PROJECTION_FIXTURE") != "1" {
		return
	}
	if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
		t.Fatal(err)
	}
}

func TestBoardDropsFinishedWindowsTeardownWithoutLifecycleWrite(t *testing.T) {
	process := exec.Command(os.Args[0], "-test.run=^TestBoardTeardownProcessFixture$")
	process.Env = append(os.Environ(), "CFO_TEARDOWN_PROJECTION_FIXTURE=1")
	input, err := process.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		input.Close()
		if process.ProcessState == nil {
			process.Process.Kill()
			process.Wait()
		}
	})
	started, exists := proc.StartTime(process.Process.Pid)
	if !exists {
		t.Fatal("could not identify the owned fixture process")
	}
	handler, h := orderBoard(t)
	meta := state.TaskMeta{ID: "task", SpawnGen: "generation-1", Project: h.Root, Worktree: h.Root, Backend: "native"}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	record := state.Lifecycle{ID: meta.ID, Generation: meta.SpawnGen, Operation: "stop-1", Action: "stop", Phase: "stopped", Teardown: []state.TeardownProcess{{PID: process.Process.Pid, Started: started, Name: "fixture.exe"}}}
	if err := state.WriteLifecycle(h.State, record); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(state.LifecyclePath(h.State, meta.ID))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := handler.Service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	index := slices.IndexFunc(snapshot.Tasks, func(task Task) bool { return task.ID == meta.ID })
	if index < 0 || !slices.Equal(snapshot.Tasks[index].Teardown, record.TeardownLabels()) {
		t.Fatalf("board hid the live owned fixture: %+v", snapshot.Tasks)
	}
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	if err := process.Wait(); err != nil {
		t.Fatal(err)
	}
	fresh, err := state.ReadLifecycle(h.State, meta.ID)
	if err != nil || len(fresh.Teardown) != 0 {
		t.Fatalf("original reader retained the finished fixture: %+v %v", fresh, err)
	}
	snapshot, err = handler.Service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range snapshot.Tasks {
		if len(task.Teardown) != 0 {
			t.Fatalf("board cached a finished identity: %+v", task)
		}
	}
	after, err := os.ReadFile(state.LifecyclePath(h.State, meta.ID))
	if err != nil || string(after) != string(before) {
		t.Fatalf("read-only projection changed the lifecycle file: %v", err)
	}
}

func TestBoardShowsEarlierSessionTeardownAfterStopRequeueAndStart(t *testing.T) {
	started, exists := proc.StartTime(os.Getpid())
	if !exists {
		t.Fatal("could not identify the test process")
	}
	handler, h := orderBoard(t)
	record := state.Lifecycle{ID: "task", Generation: "generation-1", Operation: "stop-1", Action: "stop", Phase: "stopped", Reason: "Stopped from the board", GateRun: "run-1", Updated: time.Now(), Teardown: []state.TeardownProcess{{PID: os.Getpid(), Started: started, Name: "chrome.exe"}}}
	if err := state.WriteLifecycle(h.State, record); err != nil {
		t.Fatal(err)
	}
	meta := state.TaskMeta{ID: "task", Title: "Restarted task", SpawnGen: "generation-2", Project: h.Root, Worktree: h.Root, Backend: "native"}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	snapshot, err := handler.Service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	index := slices.IndexFunc(snapshot.Tasks, func(task Task) bool { return task.ID == meta.ID })
	if index < 0 {
		t.Fatalf("no card for the new session: %+v", snapshot.Tasks)
	}
	task := snapshot.Tasks[index]
	if want := record.TeardownLabels(); !slices.Equal(task.Teardown, want) {
		t.Fatalf("new session card teardown=%v, want the earlier session's %v", task.Teardown, want)
	}
	if task.Lifecycle != nil || task.Archived || task.Phase == "stopped" || task.Generation != meta.SpawnGen {
		t.Fatalf("new session card imported the earlier session's lifecycle: %+v", task)
	}
	record.Teardown[0].Started = started.Add(-time.Hour)
	if err := state.WriteLifecycle(h.State, record); err != nil {
		t.Fatal(err)
	}
	if snapshot, err = handler.Service.Snapshot(); err != nil {
		t.Fatal(err)
	}
	for _, task := range snapshot.Tasks {
		if len(task.Teardown) != 0 {
			t.Fatalf("board retained a replaced identity: %+v", task)
		}
	}
}
