package spawn

import (
	"context"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestSpawnStillRefusesAnIDThatCollidesWithALiveTask(t *testing.T) {
	f := newFixture(t)
	// A live task keeps its metadata, and on Windows two ids differing only in
	// case would share every state file.
	if err := state.WriteTaskMeta(f.stateDir, state.TaskMeta{ID: strings.ToUpper(f.request.ID), Window: "native", Backend: "native"}); err != nil {
		t.Fatal(err)
	}

	_, err := f.service.Spawn(context.Background(), f.request)
	if err == nil || !strings.Contains(err.Error(), "conflicts case-insensitively") {
		t.Fatalf("err = %v, want the live-task collision still refused", err)
	}
}

// A task recorded in Herdr is not one this build can reach, so a switch
// refuses it by name before anything is stopped or written.
func TestSwitchRefusesATaskRecordedInHerdr(t *testing.T) {
	f := newFixture(t)
	if err := state.WriteTaskMeta(f.stateDir, state.TaskMeta{ID: "task-7", Window: "fleet:p1", Worktree: f.worktree, Project: f.project, Harness: "claude", TaskTmp: f.stateDir, Backend: "herdr", HerdrSession: "fleet", HerdrWorkspaceID: "w1", HerdrTabID: "t1", HerdrPaneID: "p1", SpawnGen: "s1"}); err != nil {
		t.Fatal(err)
	}

	_, err := f.service.Switch(context.Background(), SwitchRequest{ID: "task-7", Model: "opus"})

	if err == nil || !strings.Contains(err.Error(), `runs in backend "herdr"`) || !strings.Contains(err.Error(), "only a task in a native terminal can be switched") {
		t.Fatalf("err = %v, want the Herdr task refused by name", err)
	}
	if after, readErr := state.ReadTaskMeta(f.stateDir, "task-7"); readErr != nil || after.SpawnGen != "s1" || f.runner.calls != 0 {
		t.Errorf("record %+v, %v and %d commands after the refusal; want the task untouched", after, readErr, f.runner.calls)
	}
}

// Neither a model name nor an effort survives a change of harness: "opus"
// means nothing to codex, and pi may lack an effort claude has. A model or an
// effort the operator names explicitly is kept, a Claude goblin that names no
// model runs Opus 5.5, and a switch that changes only the effort keeps a named
// model.
func TestSwitchCarriesOnlyWhatTheNewHarnessUnderstands(t *testing.T) {
	for _, test := range []struct {
		name    string
		meta    state.TaskMeta
		request SwitchRequest
		want    switchTarget
	}{
		{"a model change keeps the effort", state.TaskMeta{Harness: "claude", Model: "opus", Effort: "high"}, SwitchRequest{Model: "sonnet"}, switchTarget{harness.Claude, "sonnet", "high"}},
		{"a new harness drops the model and the effort", state.TaskMeta{Harness: "claude", Model: "opus", Effort: "high"}, SwitchRequest{Harness: harness.Pi}, switchTarget{harness.Pi, "", ""}},
		{"an explicit effort crosses harnesses", state.TaskMeta{Harness: "claude", Model: "opus", Effort: "high"}, SwitchRequest{Harness: harness.Pi, Effort: "xhigh"}, switchTarget{harness.Pi, "", "xhigh"}},
		{"an explicit model crosses harnesses", state.TaskMeta{Harness: "claude", Model: "opus", Effort: "high"}, SwitchRequest{Harness: harness.Pi, Model: "its-model"}, switchTarget{harness.Pi, "its-model", ""}},
		{"claude with no model runs Opus 5.5", state.TaskMeta{Harness: "pi", Model: "default", Effort: "default"}, SwitchRequest{Harness: harness.Claude}, switchTarget{harness.Claude, "claude-opus-5-5", ""}},
		{"an effort change keeps a named model", state.TaskMeta{Harness: "claude", Model: "claude-sonnet-5", Effort: "high"}, SwitchRequest{Effort: "max"}, switchTarget{harness.Claude, "claude-sonnet-5", "max"}},
		{"an effort change gives an unnamed claude model Opus 5.5", state.TaskMeta{Harness: "claude", Model: "default", Effort: "high"}, SwitchRequest{Effort: "max"}, switchTarget{harness.Claude, "claude-opus-5-5", "max"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := requestedTarget(test.meta, test.request); got != test.want {
				t.Errorf("target = %+v, want %+v", got, test.want)
			}
		})
	}
}
