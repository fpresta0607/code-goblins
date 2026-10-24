package spawn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/lifecycle"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
)

type parkedSwitchGate struct {
	run   pipeline.InterruptedRun
	reads int
}

func (gate *parkedSwitchGate) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	if request.Name != "sqlite3" || len(request.Args) == 0 || request.Args[0] != "-readonly" {
		return execx.Result{}, fmt.Errorf("unexpected command against parked validation: %s %v", request.Name, request.Args)
	}
	gate.reads++
	data, err := json.Marshal([]pipeline.InterruptedRun{gate.run})
	return execx.Result{Stdout: data}, err
}

func TestAMemoryPauseAfterManualSwitchDoesNotRestartAnEarlierGate(t *testing.T) {
	// Arrange
	f := newSwitchFixture(t, harness.Control{StopCommand: "/exit", ResumeArgs: []string{"resume", "--last"}})
	gateRoot := t.TempDir()
	writeFile(t, filepath.Join(gateRoot, "state.sqlite"), "isolated database stand-in")
	parked := &parkedSwitchGate{run: pipeline.InterruptedRun{ID: "run-a", RepoID: "repo-1", Branch: f.git.branch, Status: "cancelled", Head: strings.Repeat("a", 40), Intent: "earlier validation"}}
	gate := pipeline.Reader{Root: gateRoot, Commands: parked}
	unfinished := filepath.Join(f.worktree, "unfinished.txt")
	writeFile(t, unfinished, "preserve this work")
	f.git.status = " M unfinished.txt"
	controller := lifecycle.Service{StateDir: f.stateDir, Operations: lifecycle.Operations{
		Prepare: func(_ context.Context, _ state.TaskMeta, handoff string) error {
			return os.WriteFile(handoff, []byte("retained work"), 0o600)
		},
		Stop: func(_ context.Context, meta state.TaskMeta, _ *state.Lifecycle) ([]string, error) {
			terminal, err := host.ReadRecord(f.stateDir, meta.ID)
			if errors.Is(err, os.ErrNotExist) {
				return nil, nil
			}
			if err != nil {
				return nil, err
			}
			return []string{"fixture terminal"}, host.Close(f.stateDir, terminal, nativeCloseWait)
		},
		Checkpoint: func(_ context.Context, _ state.TaskMeta, record *state.Lifecycle) error {
			if record.Pause.Reason == "overlord" {
				record.GateRun, record.GateIntent, record.GateHead = parked.run.ID, parked.run.Intent, parked.run.Head
			}
			return nil
		},
		Memory: func() (uint64, uint64, error) { return 5 << 30, 5 << 30, nil },
		Resume: func(ctx context.Context, meta state.TaskMeta, prior state.Lifecycle) error {
			if prior.GateRun != "" {
				if err := gate.RestartInterrupted(ctx, f.project, f.worktree, pipeline.InterruptedRun{ID: prior.GateRun, Branch: f.git.branch, Intent: prior.GateIntent}, nil); err != nil {
					return err
				}
			}
			_, err := f.service.Switch(ctx, SwitchRequest{ID: meta.ID, Generation: meta.SpawnGen, IsResume: true, ForceDirty: true, ResumeSession: prior.Session})
			return err
		},
		Notify: func(state.Lifecycle) error { return nil },
	}}
	oldPause, err := controller.Run(t.Context(), lifecycle.Request{ID: f.meta.ID, Generation: f.meta.SpawnGen, Operation: "old-pause", Action: "pause", Reason: "overlord", Session: "owned-session"})
	if err != nil || oldPause.GateRun != "run-a" {
		t.Fatalf("initial pause = %+v, %v", oldPause, err)
	}

	// Act
	switched, err := f.service.Switch(t.Context(), SwitchRequest{ID: f.meta.ID, Model: "gpt-9", ForceDirty: true})
	if err != nil {
		t.Fatalf("manual Switch: %v", err)
	}
	if switched.Meta.SpawnGen == f.meta.SpawnGen || switched.Meta.ResumeOperation != "" {
		t.Fatalf("manual Switch did not publish an independent generation: %+v", switched.Meta)
	}
	parked.run.ID, parked.run.Head, parked.run.Intent, parked.run.Status = "run-b", strings.Repeat("b", 40), "review the expanded change", "parked"
	beforeGate, err := gate.Interruption(t.Context(), f.project, f.git.branch)
	if err != nil {
		t.Fatal(err)
	}
	paused, err := controller.Run(t.Context(), lifecycle.Request{ID: f.meta.ID, Generation: switched.Meta.SpawnGen, Operation: "new-memory-pause", Action: "pause", Reason: "memory", Session: "owned-session"})
	if err != nil {
		t.Fatal(err)
	}
	resumed, resumeErr := controller.Run(t.Context(), lifecycle.Request{ID: f.meta.ID, Generation: switched.Meta.SpawnGen, Operation: "new-resume", Action: "resume"})

	// Assert
	if paused.GateRun != "" || paused.GateIntent != "" || paused.GateHead != "" {
		t.Errorf("new generation inherited the earlier gate: %+v", paused)
	}
	if resumeErr != nil || resumed.Phase != "running" || resumed.Generation == switched.Meta.SpawnGen {
		t.Errorf("Resume after manual Switch = %+v, %v; want a running replacement without restarting the old gate", resumed, resumeErr)
	}
	afterGate, gateErr := gate.Interruption(t.Context(), f.project, f.git.branch)
	if gateErr != nil || !reflect.DeepEqual(beforeGate, afterGate) || parked.reads != 2 {
		t.Errorf("parked validation = %+v, %v, reads=%d; want unchanged %+v and only the two assertion reads", afterGate, gateErr, parked.reads, beforeGate)
	}
	if work, err := os.ReadFile(unfinished); err != nil || string(work) != "preserve this work" {
		t.Errorf("unfinished work = %q, %v", work, err)
	}
	if launches := named(f.events(t), "env"); len(launches) != 2 {
		t.Errorf("launches = %+v, want one manual Switch and one Resume", launches)
	}
}
