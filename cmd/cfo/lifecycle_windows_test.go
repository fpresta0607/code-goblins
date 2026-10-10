package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/state"
)

type resumeRunner struct{ requests []execx.Request }

func (r *resumeRunner) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	r.requests = append(r.requests, request)
	return execx.Result{Stdout: []byte("cfo/task\n")}, nil
}

func TestResumeUsesSavedSessionOnlyWithinOneDayOfThePause(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		age         time.Duration
		wantSession string
	}{
		{name: "recent pause", age: time.Hour, wantSession: "saved-session"},
		{name: "one day old", age: 24 * time.Hour},
		{name: "older pause", age: 48 * time.Hour},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			h := primaryHomeFixture(t)
			var received spawn.SwitchRequest
			runtime := commandRuntime{switchTask: func(_ context.Context, _ home.Home, request spawn.SwitchRequest) (spawn.SwitchResult, error) {
				received = request
				return spawn.SwitchResult{}, nil
			}}
			meta := state.TaskMeta{ID: "task", SpawnGen: "generation-1", Backend: "native", Worktree: t.TempDir()}
			prior := state.Lifecycle{Session: "saved-session", Started: time.Now().Add(-testCase.age), Handoff: "retained-handoff.md", HandoffSaved: true, ResumeNote: "The Overlord answered: continue"}

			err := resumeTask(t.Context(), h, runtime, &resumeRunner{}, pipeline.Reader{}, meta, prior, nil)

			if err != nil || received.ResumeSession != testCase.wantSession || received.ResumeHandoff != prior.Handoff || received.ID != meta.ID || received.ResumeNote != prior.ResumeNote {
				t.Fatalf("resume request=%+v error=%v", received, err)
			}
		})
	}
}

func TestResumeRefusesATaskRecordedInHerdrBeforeTheGateRestarts(t *testing.T) {
	h := primaryHomeFixture(t)
	commands := &resumeRunner{}
	gate := pipeline.Reader{Root: t.TempDir(), Commands: commands}
	isSwitched := false
	runtime := commandRuntime{switchTask: func(context.Context, home.Home, spawn.SwitchRequest) (spawn.SwitchResult, error) {
		isSwitched = true
		return spawn.SwitchResult{}, nil
	}}
	meta := state.TaskMeta{ID: "task", SpawnGen: "session-1", Backend: "herdr", Project: "project", Worktree: t.TempDir()}
	prior := state.Lifecycle{ID: "task", Phase: "paused", GateRun: "run-1", GateIntent: "ship it"}

	err := resumeTask(context.Background(), h, runtime, commands, gate, meta, prior, nil)

	want := `resume: task task runs in backend "herdr"; only a task in a native terminal can resume; retire it with cfo cleanup task --force-archive`
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
	if len(commands.requests) != 0 || isSwitched {
		t.Fatalf("commands=%+v switched=%v; a Herdr task must be refused before the gate restarts or the switch runs", commands.requests, isSwitched)
	}
}

func TestResumeUsesSavedEngineOnlyForItsSessionAndKeepsFailedChoices(t *testing.T) {
	for _, test := range []struct {
		name, when, harness, generation, session string
		isFailed                                 bool
	}{
		{"same harness", "resume", "claude", "s1", "session-1", false},
		{"new harness", "resume", "codex", "s1", "", false},
		{"stale choice", "resume", "codex", "s0", "session-1", false},
		{"failed resume", "resume", "codex", "s1", "", true},
		{"pending turn-end choice paused before idle", "turn-end", "codex", "s1", "", false},
		{"failed resume keeps pending turn-end choice", "turn-end", "codex", "s1", "", true},
		{"stale turn-end choice", "turn-end", "codex", "s0", "session-1", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := primaryHomeFixture(t)
			meta := state.TaskMeta{ID: "task", SpawnGen: "s1", Harness: "claude", Backend: "native"}
			choice := state.EngineChoice{ID: meta.ID, Generation: test.generation, Harness: test.harness, Model: "selected-model", Effort: "high", When: test.when}
			if err := state.WriteEngineChoice(h.State, choice); err != nil {
				t.Fatal(err)
			}
			var request spawn.SwitchRequest
			runtime := commandRuntime{switchTask: func(_ context.Context, _ home.Home, selected spawn.SwitchRequest) (spawn.SwitchResult, error) {
				request = selected
				if test.isFailed {
					return spawn.SwitchResult{}, errors.New("launch failed")
				}
				return spawn.SwitchResult{}, nil
			}}

			err := resumeTask(context.Background(), h, runtime, &resumeRunner{}, pipeline.Reader{}, meta, state.Lifecycle{Session: "session-1", Started: time.Now().Add(-time.Hour), HandoffSaved: true, Handoff: "saved handoff"}, nil)

			if (err != nil) != test.isFailed || request.ResumeSession != test.session || request.ResumeHandoff != "saved handoff" || !request.ForceDirty {
				t.Fatalf("resume = %+v, %v", request, err)
			}
			if test.generation == meta.SpawnGen {
				if string(request.Harness) != choice.Harness || request.Model != choice.Model || request.Effort != choice.Effort {
					t.Fatalf("Resume ignored saved engine: %+v", request)
				}
			} else if request.Harness != "" || request.Model != "" || request.Effort != "" {
				t.Fatalf("Resume applied stale engine: %+v", request)
			}
			_, readErr := state.ReadEngineChoice(h.State, meta.ID)
			if test.isFailed && readErr != nil || !test.isFailed && test.generation == meta.SpawnGen && !errors.Is(readErr, os.ErrNotExist) {
				t.Fatalf("saved choice after Resume = %v", readErr)
			}
		})
	}
}

// liveGateRunner answers for a task on branch cfo/task whose latest gate run
// has status, or has no gate run with none.
type liveGateRunner struct{ status string }

func (runner liveGateRunner) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	if request.Name != "sqlite3" {
		return execx.Result{Stdout: []byte("cfo/task\n")}, nil
	}
	if runner.status == "" {
		return execx.Result{Stdout: []byte("[]")}, nil
	}
	row := fmt.Sprintf(`[{"id":"run-1","repo_id":"repo-1","branch":"cfo/task","status":%q,"head":"%s","intent":"ship it","worktree":""}]`, runner.status, strings.Repeat("a", 40))
	return execx.Result{Stdout: []byte(row)}, nil
}

// A goblin resumed while a gate run of its branch is still running is told
// which run, so it picks that run back up and starts no other beside it: a
// memory pause leaves the run alone, and a pause whose abort failed leaves it
// live too. On 2026-10-09 each resume of Murray, paused for memory with his
// run live, ended failed, and only a switch by hand brought him back.
func TestResumeTellsAGoblinWhichGateRunIsStillRunning(t *testing.T) {
	for _, testCase := range []struct {
		name, status, pausedRun string
		isTold                  bool
	}{
		{name: "a run a memory pause left alone", status: "running", isTold: true},
		{name: "a run its pause could not abort", status: "running", pausedRun: "run-1", isTold: true},
		{name: "a run that finished while it was paused", status: "completed"},
		{name: "no gate run"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			h := primaryHomeFixture(t)
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "state.sqlite"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			commands := liveGateRunner{status: testCase.status}
			var received spawn.SwitchRequest
			runtime := commandRuntime{switchTask: func(_ context.Context, _ home.Home, request spawn.SwitchRequest) (spawn.SwitchResult, error) {
				received = request
				return spawn.SwitchResult{}, nil
			}}
			meta := state.TaskMeta{ID: "task", SpawnGen: "generation-1", Backend: "native", Project: "project", Worktree: t.TempDir()}
			prior := state.Lifecycle{ID: "task", Phase: "paused", Started: time.Now().Add(-time.Hour), ResumeNote: "The Overlord answered: continue", GateRun: testCase.pausedRun, GateIntent: "ship it"}

			// Act
			err := resumeTask(t.Context(), h, runtime, commands, pipeline.Reader{Root: root, Commands: commands}, meta, prior, nil)

			// Assert
			if err != nil {
				t.Fatalf("resume = %v, want the goblin relaunched", err)
			}
			if !strings.HasPrefix(received.ResumeNote, prior.ResumeNote) {
				t.Errorf("resume note = %q, want what the pause kept for it first", received.ResumeNote)
			}
			if isTold := strings.Contains(received.ResumeNote, "run-1"); isTold != testCase.isTold {
				t.Errorf("resume note = %q, told of its run = %v, want %v", received.ResumeNote, isTold, testCase.isTold)
			}
			if testCase.isTold && strings.ContainsAny(strings.TrimPrefix(received.ResumeNote, prior.ResumeNote+"\n"), "\r\n") {
				t.Errorf("resume note = %q, want the run named on one line of its own", received.ResumeNote)
			}
		})
	}
}

// resumedGateRunner is a paused task's machine as Resume finds it: a gate run
// of its branch that the pause cancelled, which a new run replaces once
// no-mistakes is asked to start one.
type resumedGateRunner struct {
	isReplaced bool
	native     []execx.Request
}

func (runner *resumedGateRunner) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	command := strings.Join(request.Args, " ")
	switch {
	case request.Name == "sqlite3" && strings.Contains(command, "SELECT default_branch FROM repos"):
		return execx.Result{Stdout: []byte(`[{"default_branch":"main"}]`)}, nil
	case request.Name == "sqlite3":
		id, status := "run-1", "cancelled"
		if runner.isReplaced {
			id, status = "run-2", "running"
		}
		row := fmt.Sprintf(`[{"id":%q,"repo_id":"repo-1","branch":"cfo/task","status":%q,"head":"%s","intent":"ship safely","worktree":""}]`, id, status, strings.Repeat("a", 40))
		return execx.Result{Stdout: []byte(row)}, nil
	case request.Name == "git" && command == "ls-remote --symref origin HEAD":
		return execx.Result{Stdout: []byte("ref: refs/heads/main\tHEAD\n0123456789abcdef0123456789abcdef01234567\tHEAD\n")}, nil
	case request.Name == "git":
		return execx.Result{Stdout: []byte("cfo/task\n")}, nil
	case request.Name == "no-mistakes":
		runner.native = append(runner.native, request)
		if strings.HasPrefix(command, "axi run ") {
			runner.isReplaced = true
			return execx.Result{ExitCode: 1, Stderr: []byte("bounded wait elapsed")}, nil
		}
		return execx.Result{}, nil
	}
	return execx.Result{}, fmt.Errorf("unexpected resume command: %s %s", request.Name, command)
}

// Resume starts a run in place of the one its pause cancelled, and that start
// goes past cfo pipeline run. Under a policy whose gate runs on its task's own
// harness it carries the same launch selection, on the harness the goblin
// comes back on, or the replacement would run whatever the machine's chain
// names.
func TestResumeRestartsAGateOnTheHarnessTheGoblinComesBackOn(t *testing.T) {
	for _, test := range []struct {
		name   string
		choice *state.EngineChoice
		want   string
		other  string
	}{
		{name: "its own harness", want: "claude", other: "codex"},
		{name: "the harness the resume switches it to", choice: &state.EngineChoice{Harness: "codex", Model: "gpt-6.1-sol", Effort: "high", When: "resume"}, want: "codex", other: "claude"},
	} {
		t.Run(test.name, func(t *testing.T) {
			policy := versionSixPolicy(t, pipeline.Reviewer{})
			h, nm := gateHome(t, policy, operatorMachineConfig)
			_, frozen := gatedTask(t, h, "task", policy, claudeGoblin)
			meta, err := state.ReadTaskMeta(h.State, "task")
			if err != nil {
				t.Fatal(err)
			}
			meta.Backend, meta.SpawnGen = "native", "generation-1"
			if test.choice != nil {
				test.choice.ID, test.choice.Generation = meta.ID, meta.SpawnGen
				if err := state.WriteEngineChoice(h.State, *test.choice); err != nil {
					t.Fatal(err)
				}
			}
			commands := &resumedGateRunner{}
			runtime := commandRuntime{switchTask: func(context.Context, home.Home, spawn.SwitchRequest) (spawn.SwitchResult, error) {
				return spawn.SwitchResult{}, nil
			}}
			prior := state.Lifecycle{ID: "task", Phase: "paused", Started: time.Now().Add(-time.Hour), GateRun: "run-1", GateIntent: "ship safely"}

			if err := resumeTask(t.Context(), h, runtime, commands, pipeline.Reader{Root: nm, Commands: commands}, meta, prior, nil); err != nil {
				t.Fatalf("resume: %v", err)
			}

			var started []execx.Request
			for _, request := range commands.native {
				if strings.HasPrefix(strings.Join(request.Args, " "), "axi run ") {
					started = append(started, request)
				}
			}
			if len(started) != 1 {
				t.Fatalf("replacement runs started = %+v, want one", started)
			}
			args := started[0].Args
			if len(args) != 12 || strings.Join(args[:6], " ") != "axi run --intent ship safely --wait 45s" || args[6] != "--launch-nonce" || args[8] != "--validation-generation" || args[9] != frozen.Hash || args[10] != "--launch-assertion" {
				t.Fatalf("replacement argv = %q, want it started under a launch selection", args)
			}
			raw, err := os.ReadFile(args[11])
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(raw), `"harness":"`+test.want+`"`) || strings.Contains(string(raw), test.other) || !strings.Contains(string(raw), `"apply":true`) {
				t.Fatalf("launch selection = %s, want %s for every role and no %s", raw, test.want, test.other)
			}
		})
	}
}

func TestResumeRestartsAGateFrozenBeforeVersionSixAsItAlwaysDid(t *testing.T) {
	policy, err := pipeline.Load(filepath.Join("..", "..", "config", "pipeline.json"))
	if err != nil {
		t.Fatal(err)
	}
	if policy.Version > 5 {
		t.Skipf("the checked-in policy is version %d, which carries a launch selection", policy.Version)
	}
	h, nm := gateHome(t, policy, operatorMachineConfig)
	gatedTask(t, h, "task", policy, claudeGoblin)
	meta, err := state.ReadTaskMeta(h.State, "task")
	if err != nil {
		t.Fatal(err)
	}
	meta.Backend, meta.SpawnGen = "native", "generation-1"
	commands := &resumedGateRunner{}
	runtime := commandRuntime{switchTask: func(context.Context, home.Home, spawn.SwitchRequest) (spawn.SwitchResult, error) {
		return spawn.SwitchResult{}, nil
	}}
	prior := state.Lifecycle{ID: "task", Phase: "paused", Started: time.Now().Add(-time.Hour), GateRun: "run-1", GateIntent: "ship safely"}

	if err := resumeTask(t.Context(), h, runtime, commands, pipeline.Reader{Root: nm, Commands: commands}, meta, prior, nil); err != nil {
		t.Fatalf("resume: %v", err)
	}

	for _, request := range commands.native {
		if command := strings.Join(request.Args, " "); strings.HasPrefix(command, "axi run ") && command != "axi run --intent ship safely --wait 45s" {
			t.Fatalf("replacement = %q, want no launch selection under a policy that fixes its own chain", command)
		}
	}
}
