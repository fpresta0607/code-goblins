package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	t.Setenv("CFO_HOME", h.Root)
	t.Setenv("CFO_STATE_OVERRIDE", h.State)
	nativeCFOTerminal(t, h.State)
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
	board, err := supervisor.Start(t.Context(), h, supervisor.Options{Example: true, CFO: &supervisor.CFOConnection{State: h.State}})
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

// --in names where the Overlord answered, which only a record of that answer
// takes: cfo answer without --record-only delivers the CFO's own choice.
func TestAnswerCommandTakesWhereTheOverlordAnsweredOnlyForARecord(t *testing.T) {
	// Arrange
	runtime := commandRuntime{resolveHome: func() (home.Home, error) {
		t.Fatal("a refused command resolved the home")
		return home.Home{}, nil
	}}
	var stdout, stderr bytes.Buffer

	// Act
	exit := runAnswer([]string{"herdr-strays-20260929", "--option", "Stop them", "--in", "chat"}, &stdout, &stderr, runtime)

	// Assert
	if exit != 2 || !strings.Contains(stderr.String(), "--in goes with --record-only") {
		t.Fatalf("exit=%d stderr=%q, want 2 naming --in goes with --record-only", exit, stderr.String())
	}
}

// cfo answer <task-id> "<option>" exited 2 printing nothing and recording
// nothing, so it read as an answer given while the goblin's question stayed
// open (#851). Every argument shape cfo answer cannot act on is refused
// before the home is read, saying why, with its usage.
func TestAnswerCommandRefusesAShapeItCannotActOnWithItsUsage(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		why  string
	}{
		{"a task id and a bare option", []string{"cg-trains-health-answer", "Hold mine"}, `unexpected argument "Hold mine"`},
		{"a wake sequence and a bare option", []string{"1002017", "Hold mine"}, `unexpected argument "Hold mine"`},
		{"an unknown id and a bare option", []string{"4242", "Hold mine", "--note", "go"}, `unexpected argument "Hold mine"`},
		{"a task id with its option", []string{"cg-trains-health-answer", "--option", "Hold mine"}, "cg-trains-health-answer names neither a goblin question nor its notify's wake sequence"},
		{"no id", []string{"--option", "Hold mine"}, "a question ID or a goblin notify's wake sequence is required"},
		{"no option", []string{"1002017"}, "--option is required"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			runtime := commandRuntime{resolveHome: func() (home.Home, error) {
				t.Fatal("a refused command resolved the home")
				return home.Home{}, nil
			}}
			var stdout, stderr bytes.Buffer

			// Act
			exit := runAnswer(test.args, &stdout, &stderr, runtime)

			// Assert
			if exit != 2 || !strings.Contains(stderr.String(), test.why) || !strings.Contains(stderr.String(), "usage: cfo answer <question-id|wake-sequence> --option <choice>") {
				t.Fatalf("exit=%d stderr=%q, want 2 saying %q with the usage", exit, stderr.String(), test.why)
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want nothing: only a recorded answer prints", stdout.String())
			}
		})
	}
}
