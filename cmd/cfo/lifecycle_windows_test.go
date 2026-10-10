package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lifecycle"
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

// refusingGateRunner is a paused gated task's machine as a Resume its relaunch
// refuses finds it: the latest gate run of its branch, cancelled, a
// no-mistakes that recovers nothing, and git naming the branch unless the
// worktree is detached.
type refusingGateRunner struct {
	run, intent string
	isDetached  bool
}

func (runner refusingGateRunner) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	command := strings.Join(request.Args, " ")
	switch {
	case request.Name == "sqlite3" && strings.Contains(command, "SELECT default_branch FROM repos"):
		return execx.Result{Stdout: []byte(`[{"default_branch":"main"}]`)}, nil
	case request.Name == "sqlite3":
		row := fmt.Sprintf(`[{"id":%q,"repo_id":"repo-1","branch":"cfo/task","status":"cancelled","head":"%s","intent":%q,"worktree":""}]`, runner.run, strings.Repeat("a", 40), runner.intent)
		return execx.Result{Stdout: []byte(row)}, nil
	case request.Name == "git" && command == "ls-remote --symref origin HEAD":
		return execx.Result{Stdout: []byte("ref: refs/heads/main\tHEAD\n0123456789abcdef0123456789abcdef01234567\tHEAD\n")}, nil
	case request.Name == "git" && runner.isDetached:
		return execx.Result{ExitCode: 128, Stderr: []byte("fatal: ref HEAD is not a symbolic ref")}, nil
	case request.Name == "git":
		return execx.Result{Stdout: []byte("cfo/task\n")}, nil
	case request.Name == "no-mistakes":
		return execx.Result{ExitCode: 1, Stderr: []byte("the daemon is not running")}, nil
	}
	return execx.Result{}, fmt.Errorf("unexpected resume command: %s %s", request.Name, command)
}

// On 2026-10-10 cfo resume pd-whats-new was refused for a gate policy older
// than the machine's, and the refusal took the task out of its pause, which
// the CFO put back by hand. Each refusal a resume's relaunch can give before
// it starts a terminal leaves the task paused as it was, with its reason, its
// condition and the gate run its pause interrupted, and writes one line into
// its status log.
func TestEachRefusalOfAResumesRelaunchLeavesTheTaskPausedAsItWas(t *testing.T) {
	type refused struct {
		h        home.Home
		meta     state.TaskMeta
		paused   state.Lifecycle
		commands execx.Runner
		switched error
	}
	tests := []struct {
		name         string
		isVersionSix bool
		goblin       pipeline.Reviewer
		arrange      func(*testing.T, *refused)
		want         string
		isSwitched   bool
	}{
		{name: "a task recorded in Herdr", goblin: claudeGoblin, arrange: func(_ *testing.T, f *refused) {
			f.meta.Backend, f.meta.HerdrSession, f.meta.HerdrWorkspaceID, f.meta.HerdrTabID, f.meta.HerdrPaneID = "herdr", "goblins", "w1", "t1", "p1"
		}, want: "only a task in a native terminal can resume"},
		{name: "a saved engine that cannot be read", goblin: claudeGoblin, arrange: func(t *testing.T, f *refused) {
			path := filepath.Join(f.h.State, "engine", f.meta.ID+".json")
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, want: "unexpected end of JSON input"},
		{name: "a paused validation branch that cannot be read", goblin: claudeGoblin, arrange: func(_ *testing.T, f *refused) {
			f.commands = refusingGateRunner{run: "run-1", intent: "ship safely", isDetached: true}
		}, want: "cannot read the paused validation branch"},
		{name: "a paused validation with no saved intent", goblin: claudeGoblin, arrange: func(_ *testing.T, f *refused) { f.paused.GateIntent = "" }, want: "paused validation has no saved intent"},
		{name: "another validation run on its branch", goblin: claudeGoblin, arrange: func(_ *testing.T, f *refused) {
			f.commands = refusingGateRunner{run: "run-9", intent: "ship something else"}
		}, want: "another validation run owns the branch"},
		{name: "a frozen policy that differs from its record", goblin: claudeGoblin, arrange: func(_ *testing.T, f *refused) { f.meta.PipelineHash = strings.Repeat("0", 64) }, want: "frozen policy differs from its record, as a migration that stopped part way leaves it. Run cfo pipeline migrate task, then resume it"},
		{name: "a gate policy older than the machine's", goblin: claudeGoblin, arrange: func(t *testing.T, f *refused) {
			older := legacyPipelineSelection(t, "ordinary")
			if err := older.Save(filepath.Join(f.meta.TaskTmp, "pipeline.json")); err != nil {
				t.Fatal(err)
			}
			f.meta.PipelineHash, f.meta.PipelineClass = older.Hash, older.Class
		}, want: "Run cfo pipeline migrate task, then resume it"},
		{name: "a gate that cannot run on its harness", isVersionSix: true, goblin: pipeline.Reviewer{Harness: "pi", Model: "kimi", Effort: "high"}, want: "Task task would come back on pi, so resume it on claude or codex"},
		{name: "a paused validation that cannot be recovered", goblin: claudeGoblin, want: "recover paused validation: the daemon is not running"},
		{name: "a relaunch its switch refuses", goblin: claudeGoblin, arrange: func(_ *testing.T, f *refused) {
			f.commands, f.switched = &resumedGateRunner{}, errors.New("switch: validate harness claude: claude is not installed")
		}, want: "claude is not installed", isSwitched: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			policy, err := pipeline.Load(filepath.Join("..", "..", "config", "pipeline.json"))
			if err != nil {
				t.Fatal(err)
			}
			if test.isVersionSix {
				policy = versionSixPolicy(t, pipeline.Reviewer{})
			}
			h, nm := gateHome(t, policy, operatorMachineConfig)
			gatedTask(t, h, "task", policy, test.goblin)
			meta, err := state.ReadTaskMeta(h.State, "task")
			if err != nil {
				t.Fatal(err)
			}
			meta.Backend, meta.SpawnGen = "native", "generation-1"
			at := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
			condition, err := state.NewPauseCondition("dependency", "pr:https://github.com/northwind/api/pull/7", at)
			if err != nil {
				t.Fatal(err)
			}
			f := &refused{h: h, meta: meta, commands: refusingGateRunner{run: "run-1", intent: "ship safely"}, paused: state.Lifecycle{ID: meta.ID, Generation: meta.SpawnGen, RequestGeneration: meta.SpawnGen, Operation: "pause-1", Action: "pause", Phase: "paused", Started: at, Updated: at, Reason: "dependency", Pause: &condition, Session: "session-1", GateRun: "run-1", GateIntent: "ship safely", GateHead: strings.Repeat("a", 40), NoticeSent: true}}
			if test.arrange != nil {
				test.arrange(t, f)
			}
			if err := state.WriteTaskMeta(h.State, f.meta); err != nil {
				t.Fatal(err)
			}
			if err := state.WriteLifecycle(h.State, f.paused); err != nil {
				t.Fatal(err)
			}
			isSwitched := false
			runtime := commandRuntime{switchTask: func(context.Context, home.Home, spawn.SwitchRequest) (spawn.SwitchResult, error) {
				isSwitched = true
				return spawn.SwitchResult{}, f.switched
			}}
			service := lifecycle.Service{StateDir: h.State, Operations: lifecycle.Operations{
				Memory: func() (uint64, uint64, error) { return 5 << 30, 5 << 30, nil },
				Resume: func(ctx context.Context, meta state.TaskMeta, prior state.Lifecycle) error {
					return resumeTask(ctx, h, runtime, f.commands, pipeline.Reader{Root: nm, Commands: f.commands}, meta, prior, nil)
				},
				Notify: func(state.Lifecycle) error { return nil },
			}}

			// Act
			_, refusal := service.Run(t.Context(), lifecycle.Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "resume-1", Action: "resume", Reason: "Requested by the operator"})

			// Assert
			if refusal == nil || !strings.Contains(refusal.Error(), "resume refused, and the task is left as it was: ") || !strings.Contains(refusal.Error(), test.want) {
				t.Errorf("refusal = %v, want a refused resume that says %q", refusal, test.want)
			}
			if isSwitched != test.isSwitched {
				t.Errorf("the relaunch reached its switch: %v, want %v", isSwitched, test.isSwitched)
			}
			left, err := state.ReadLifecycle(h.State, meta.ID)
			if err != nil || !reflect.DeepEqual(left, f.paused) {
				t.Errorf("record after the refusal = %+v %v\nwant the pause, its reason, its condition and its gate run as they were: %+v", left, err, f.paused)
			}
			lines, _ := state.TailStatus(h.State, meta.ID, 50)
			if len(lines) != 1 || !strings.Contains(lines[0], " lifecycle-refused: resume refused, and the task is left as it was: ") || !strings.Contains(lines[0], test.want) {
				t.Errorf("status log = %q, want one line saying the resume was refused and why", lines)
			}
		})
	}
}
