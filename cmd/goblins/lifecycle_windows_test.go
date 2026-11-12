package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
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
	return execx.Result{Stdout: []byte("ceo/task\n")}, nil
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

func TestResumeRefusesATaskRecordedInHerdrBeforeRelaunch(t *testing.T) {
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

	want := `resume: task task runs in backend "herdr"; only a task in a native terminal can resume; retire it with goblins cleanup task --force-archive`
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

// liveGateRunner answers for a task on branch ceo/task whose latest gate run
// has status, or has no gate run with none.
type liveGateRunner struct {
	status     string
	queryError error
}

func (runner liveGateRunner) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	if request.Name != "sqlite3" {
		return execx.Result{Stdout: []byte("ceo/task\n")}, nil
	}
	if runner.queryError != nil {
		return execx.Result{}, runner.queryError
	}
	if runner.status == "" {
		return execx.Result{Stdout: []byte("[]")}, nil
	}
	row := fmt.Sprintf(`[{"id":"run-1","repo_id":"repo-1","branch":"ceo/task","status":%q,"head":"%s","intent":"ship it","worktree":""}]`, runner.status, strings.Repeat("a", 40))
	return execx.Result{Stdout: []byte(row)}, nil
}

// A goblin resumed while a gate run of its branch is still running is told
// which run, so it picks that run back up and starts no other beside it: a
// memory pause leaves the run alone, and a pause whose abort failed leaves it
// live too. On 2026-10-09 each resume of Murray, paused for memory with his
// run live, ended failed, and only a switch by hand brought him back.
func TestResumeTellsAGoblinWhichGateRunIsStillRunning(t *testing.T) {
	for _, testCase := range []struct {
		name, status, pausedRun, refusal string
		queryError                       error
		isTold                           bool
	}{
		{name: "a run a memory pause left alone", status: "running", pausedRun: "run-1", isTold: true},
		{name: "a run its pause could not abort", status: "running", pausedRun: "run-1", isTold: true},
		{name: "a run that finished while it was paused", status: "completed", pausedRun: "run-1", isTold: true},
		{name: "an unbound live run", status: "running", refusal: "no saved validation identity"},
		{name: "an unbound terminal run", status: "failed", refusal: "no saved validation identity"},
		{name: "unreadable unbound validation", queryError: errors.New("database locked"), refusal: "state query failed"},
		{name: "no gate run"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			h := primaryHomeFixture(t)
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "state.sqlite"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			commands := liveGateRunner{status: testCase.status, queryError: testCase.queryError}
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
			if testCase.refusal != "" {
				if err == nil || !strings.Contains(err.Error(), testCase.refusal) || received.ID != "" {
					t.Fatalf("resume = %v, launch = %+v, want refusal %q", err, received, testCase.refusal)
				}
				return
			}
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

type resumedGateRunner struct{ native []execx.Request }

func (runner *resumedGateRunner) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	switch request.Name {
	case "sqlite3":
		row := fmt.Sprintf(`[{"id":"run-1","repo_id":"repo-1","branch":"ceo/task","status":"cancelled","head":"%s","intent":"ship safely","worktree":""}]`, strings.Repeat("a", 40))
		return execx.Result{Stdout: []byte(row)}, nil
	case "git":
		return execx.Result{Stdout: []byte("ceo/task\n")}, nil
	case "no-mistakes":
		runner.native = append(runner.native, request)
		return execx.Result{}, errors.New("resume must not start validation")
	}
	return execx.Result{}, fmt.Errorf("unexpected resume command: %s %v", request.Name, request.Args)
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
		row := fmt.Sprintf(`[{"id":%q,"repo_id":"repo-1","branch":"ceo/task","status":"cancelled","head":"%s","intent":%q,"worktree":""}]`, runner.run, strings.Repeat("a", 40), runner.intent)
		return execx.Result{Stdout: []byte(row)}, nil
	case request.Name == "git" && command == "ls-remote --symref origin HEAD":
		return execx.Result{Stdout: []byte("ref: refs/heads/main\tHEAD\n0123456789abcdef0123456789abcdef01234567\tHEAD\n")}, nil
	case request.Name == "git" && runner.isDetached:
		return execx.Result{ExitCode: 128, Stderr: []byte("fatal: ref HEAD is not a symbolic ref")}, nil
	case request.Name == "git":
		return execx.Result{Stdout: []byte("ceo/task\n")}, nil
	case request.Name == "no-mistakes":
		return execx.Result{ExitCode: 1, Stderr: []byte("the daemon is not running")}, nil
	}
	return execx.Result{}, fmt.Errorf("unexpected resume command: %s %s", request.Name, command)
}

// On 2026-10-10 goblins resume pd-whats-new was refused for a gate policy older
// than the machine's, and the refusal took the task out of its pause, which
// the CEO put back by hand. Each refusal a resume's relaunch can give before
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
		name       string
		goblin     pipeline.Reviewer
		arrange    func(*testing.T, *refused)
		want       string
		isSwitched bool
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
		{name: "another validation run on its branch", goblin: claudeGoblin, arrange: func(_ *testing.T, f *refused) {
			f.commands = refusingGateRunner{run: "run-9", intent: "ship something else"}
		}, want: "another validation run owns the branch"},
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

// A task whose record names no brief must still come back with its brief under
// the home, the path a switch and a comeback already use. On 2026-10-24 a
// resumed worker was sent to a relative data/<id>/brief.md that resolved inside
// its own worktree, where no brief is. This proves the caller wiring and the
// fallback together, from a worker worktree as the working folder, with a real
// brief file under the home.
func TestResumeGivesAWorkerWithoutARecordedBriefItsRealBriefUnderTheHome(t *testing.T) {
	// Arrange
	h, _ := gateHome(t, versionSixPolicy(t, pipeline.Reviewer{}), operatorMachineConfig)
	h.Data = filepath.Join(h.Root, "data")
	worktree := filepath.Join(h.Root, "project", ".worktrees", "gb-task")
	if err := os.MkdirAll(worktree, 0o700); err != nil {
		t.Fatal(err)
	}
	taskTmp := filepath.Join(h.State, "tasktmp", "task")
	if err := os.MkdirAll(taskTmp, 0o700); err != nil {
		t.Fatal(err)
	}
	meta := state.TaskMeta{ID: "task", Mode: "direct-PR", Worktree: worktree, Project: filepath.Join(h.Root, "project"), TaskTmp: taskTmp, Harness: "claude", Model: "claude-opus-5-5", Effort: "xhigh", Backend: "native", SpawnGen: "generation-1"}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	brief := filepath.Join(h.Data, "task", "brief.md")
	if err := os.MkdirAll(filepath.Dir(brief), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(brief, []byte("# Brief task\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(worktree); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	var switched []spawn.SwitchRequest
	runtime := commandRuntime{switchTask: func(_ context.Context, _ home.Home, request spawn.SwitchRequest) (spawn.SwitchResult, error) {
		switched = append(switched, request)
		return spawn.SwitchResult{}, nil
	}}
	prior := state.Lifecycle{ID: "task", Phase: "paused", Started: time.Now().Add(-time.Hour)}

	// Act
	if err := resumeTask(t.Context(), h, runtime, &resumedGateRunner{}, pipeline.Reader{Root: h.State, Commands: &resumedGateRunner{}}, meta, prior, nil); err != nil {
		t.Fatalf("resume: %v", err)
	}

	// Assert
	if len(switched) != 1 {
		t.Fatalf("switches = %d, want the worker resumed once", len(switched))
	}
	if switched[0].BriefPath != brief {
		t.Fatalf("resume brief = %q, want the home fallback %q", switched[0].BriefPath, brief)
	}
	if !filepath.IsAbs(switched[0].BriefPath) {
		t.Fatalf("resume brief %q is not absolute", switched[0].BriefPath)
	}
	if _, err := os.Stat(switched[0].BriefPath); err != nil {
		t.Fatalf("the resume brief %q is not a real file: %v", switched[0].BriefPath, err)
	}
	if want := filepath.Join(h.Data, "task", "brief.md"); switched[0].BriefPath != want {
		t.Fatalf("resume brief = %q, want it resolved under the home as %q", switched[0].BriefPath, want)
	}
}

// goblins kill retires a paused task whose project checkout was removed after it
// paused, as a gate proof's fixture repository under the home's scratch folder
// was on 2026-10-27, and keeps what is left of it: its worktree folder, whose
// .git file names a repository that no longer exists, with its files, and its
// record. Before, the kill refused with "read the task branch before
// identifying its gate", so the task could never be retired (issue #1578).
// The run its pause saved, still open in the gate, does not hold the kill's
// own retirement either: with the checkout gone the kill can abort nothing,
// so a cleanup that refused around that run would leave the task for good.
func TestKillRetiresATaskWhoseProjectCheckoutIsGone(t *testing.T) {
	for _, tc := range []struct {
		name           string
		isSavedRunOpen bool
	}{
		{name: "a pause that saved no run"},
		{name: "a pause whose saved run is still open", isSavedRunOpen: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			h, meta := killedTask(t)
			gateRoot := t.TempDir()
			gateDatabase := filepath.Join(gateRoot, "state.sqlite")
			savedRun := ""
			if tc.isSavedRunOpen {
				savedRun = "run-1"
				schema := fmt.Sprintf(`CREATE TABLE repos(id TEXT,working_path TEXT);
CREATE TABLE runs(id TEXT,repo_id TEXT,branch TEXT,status TEXT,head_sha TEXT,last_pushed_sha TEXT,intent TEXT,worktree_dir TEXT,created_at INTEGER,submitted_head_sha TEXT,custody_returned_at INTEGER,review_approved_head_sha TEXT,pr_url TEXT,pr_state TEXT,ci_ready_at INTEGER,terminal_head_verified_at INTEGER);
INSERT INTO repos VALUES('repo-1','%s');
INSERT INTO runs(id,repo_id,branch,status,head_sha,last_pushed_sha,intent,worktree_dir,created_at) VALUES('run-1','repo-1','fix/g1','running','%s','','','',1);`, strings.ReplaceAll(filepath.ToSlash(meta.Project), "'", "''"), strings.Repeat("a", 40))
				if output, err := exec.Command("sqlite3", gateDatabase, schema).CombinedOutput(); err != nil {
					t.Fatalf("create gate fixture: %v %s", err, output)
				}
			} else if err := os.WriteFile(gateDatabase, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("NM_HOME", gateRoot)
			t.Setenv("GOBLINS_VERIFY_DIR", t.TempDir())
			at := time.Now().UTC().Add(-time.Hour)
			if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: meta.ID, Generation: meta.SpawnGen, RequestGeneration: meta.SpawnGen, Operation: "pause-1", Action: "pause", Phase: "paused", Started: at, Updated: at, Pause: &state.PauseCondition{Reason: "overlord", At: at}, GateRun: savedRun, NoticeSent: true}); err != nil {
				t.Fatal(err)
			}
			leftover := filepath.Join(meta.Worktree, "README.md")
			if err := os.WriteFile(leftover, []byte("the goblin's work"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(meta.Project); err != nil {
				t.Fatal(err)
			}

			// Act
			record, err := defaultTaskLifecycle(t.Context(), h, lifecycle.Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "kill-1", Action: "stop", Reason: "Requested by the operator", IsWatched: true}, "")

			// Assert
			if err != nil || record.Phase != "stopped" {
				t.Fatalf("the kill = phase %q, %v, want the task retired\nproblems: %s", record.Phase, err, strings.Join(record.Problems, "; "))
			}
			if _, err := state.ReadTaskMeta(h.State, meta.ID); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("the task record after the kill: %v, want it retired", err)
			}
			if content, err := os.ReadFile(leftover); err != nil || string(content) != "the goblin's work" {
				t.Errorf("the worktree's file after the kill = %q, %v, want it kept", content, err)
			}
			if kept := strings.Join(record.Kept, "; "); !strings.Contains(kept, meta.Worktree) || !strings.Contains(kept, "project checkout "+meta.Project+" is gone") {
				t.Errorf("the kill kept %q, want it to name the worktree it left in place and that its project checkout is gone", record.Kept)
			}
		})
	}
}
