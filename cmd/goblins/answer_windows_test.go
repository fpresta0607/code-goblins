package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

func TestAnswerCommandReportsADeadPausedHostAnswerAsQueued(t *testing.T) {
	// Arrange
	h := registerHome(t)
	h.Data = filepath.Join(h.Root, "data")
	t.Setenv("GOBLINS_HOME", h.Root)
	t.Setenv("GOBLINS_STATE_OVERRIDE", h.State)
	nativeCEOTerminal(t, h.State)
	if _, err := lock.Acquire(h.State); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Release(h.State) })
	if _, err := supervisor.Register(h.State, "claude", "answer-fixture"); err != nil {
		t.Fatal(err)
	}
	meta := state.TaskMeta{ID: "paused-task", Worktree: t.TempDir(), Project: t.TempDir(), Harness: "claude", Kind: "ship", Mode: "local-only", Yolo: "no", SpawnGen: "fixture-generation", Backend: "native"}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	hostThisProcess(t, h.State, meta.ID)
	notified, err := wake.Append(h.State, "notify", meta.ID, "blocked: Which store? options: Postgres | SQLite (Recommended)")
	if err != nil {
		t.Fatal(err)
	}
	if err := supervisor.SurfaceNotify(h.State, meta.ID, notified, notified.Detail, "", "", nil, nil); err != nil {
		t.Fatal(err)
	}
	condition, err := state.NewPauseCondition("memory", "", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: meta.ID, Generation: meta.SpawnGen, RequestGeneration: meta.SpawnGen, Operation: "pause-memory", Action: "pause", Phase: "paused", Pause: &condition}); err != nil {
		t.Fatal(err)
	}
	record, err := host.ReadRecord(h.State, meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	client, err := host.Dial(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.CloseTerminal(); err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(10 * time.Millisecond) {
		if _, err := host.ReadRecord(h.State, meta.ID); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the disposable paused host did not end")
		}
	}
	board, err := supervisor.Start(t.Context(), h, supervisor.Options{Example: true, CEO: &supervisor.CEOConnection{State: h.State}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(board.Close)
	var stdout, stderr bytes.Buffer
	runtime := commandRuntime{resolveHome: func() (home.Home, error) { return h, nil }}

	// Act
	exit := runAnswer([]string{fmt.Sprint(notified.Seq), "--option", "sqlite", "--note", "keep it local"}, &stdout, &stderr, runtime)

	// Assert
	want := fmt.Sprintf("queued answer %d: SQLite (retained for the goblin's resume or its next tool call; do not send it again)\n", notified.Seq)
	if exit != 0 || stderr.Len() != 0 || stdout.String() != want {
		t.Fatalf("exit=%d stdout=%q stderr=%q; want a successful queued answer: %q", exit, stdout.String(), stderr.String(), want)
	}
	retained, err := state.ReadLifecycle(h.State, meta.ID)
	if err != nil || retained.ResumeNote != fmt.Sprintf("decision %d: SQLite. keep it local", notified.Seq) {
		t.Fatalf("retained = %+v, %v; want the CLI answer in the existing resume note", retained, err)
	}
}
