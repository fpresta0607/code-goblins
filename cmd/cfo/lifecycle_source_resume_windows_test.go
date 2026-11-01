package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lifecycle"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/state"
)

type sourceResumeCommands struct {
	native     []execx.Request
	queryError error
}

func (commands *sourceResumeCommands) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	if request.Name == "sqlite3" && request.Args[0] == "-readonly" && commands.queryError != nil {
		return execx.Result{}, commands.queryError
	}
	if request.Name == "no-mistakes" {
		commands.native = append(commands.native, request)
		return execx.Result{ExitCode: 1, Stderr: []byte("validation must be requested explicitly")}, nil
	}
	return (execx.OSRunner{}).Run(ctx, request)
}

func TestPublicResumeRestoresSourceWithoutReplacingValidation(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "fixture-github-credential")
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 is needed for persisted gate evidence")
	}
	for _, test := range []struct {
		name, status, refusal                                                      string
		isForeign, isUnreadable, isLowMemory, isAllowanceDenied, isProviderRefused bool
	}{
		{name: "cancelled", status: "cancelled"},
		{name: "failed", status: "failed"},
		{name: "completed", status: "completed"},
		{name: "interrupted CI monitor", status: "ci_monitor_interrupted"},
		{name: "live original", status: "running"},
		{name: "pending original", status: "pending"},
		{name: "foreign run with identical intent", status: "running", isForeign: true, refusal: "another validation run owns"},
		{name: "unreadable gate state", status: "cancelled", isUnreadable: true, refusal: "state query failed"},
		{name: "memory admission", status: "failed", isLowMemory: true, refusal: "Resume needs at least 5 GB"},
		{name: "allowance admission", status: "failed", isAllowanceDenied: true, refusal: "provider allowance exhausted"},
		{name: "provider safety refusal", status: "failed", isProviderRefused: true, refusal: "provider refused this session"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			h := primaryHomeFixture(t)
			profile := t.TempDir()
			for _, name := range []string{"USERPROFILE", "HOME", "APPDATA", "LOCALAPPDATA"} {
				t.Setenv(name, profile)
			}
			t.Setenv("CFO_HOME", h.Root)
			t.Setenv("CFO_STATE_OVERRIDE", h.State)
			nativeRoot := t.TempDir()
			t.Setenv("NM_HOME", nativeRoot)
			if effective, err := os.UserHomeDir(); err != nil || effective != profile {
				t.Fatalf("effective home = %q, %v", effective, err)
			}
			commands := &sourceResumeCommands{}
			git := func(args ...string) string {
				t.Helper()
				result, err := commands.Run(t.Context(), execx.Request{Dir: h.Root, Name: "git", Args: args})
				if err != nil || result.ExitCode != 0 {
					t.Fatalf("git %v: %v %s", args, err, result.Stderr)
				}
				return strings.TrimSpace(string(result.Stdout))
			}
			git("checkout", "-b", "fix/source-resume")
			git("add", "AGENTS.md")
			git("commit", "-m", "test: retain source")
			head := git("rev-parse", "HEAD")
			unfinished := filepath.Join(h.Root, "unfinished.txt")
			if err := os.WriteFile(unfinished, []byte("author repair in progress"), 0o600); err != nil {
				t.Fatal(err)
			}
			meta := state.TaskMeta{ID: "task", SpawnGen: "generation-1", Backend: "native", Mode: "no-mistakes", Project: h.Root, Worktree: h.Root, TaskTmp: filepath.Join(h.State, "tasktmp", "task")}
			if err := os.MkdirAll(meta.TaskTmp, 0o700); err != nil {
				t.Fatal(err)
			}
			frozen, err := versionSixPolicy(t, pipeline.Reviewer{}).Select("high-risk")
			if err != nil {
				t.Fatal(err)
			}
			policyPath := filepath.Join(meta.TaskTmp, "pipeline.json")
			if err := frozen.Save(policyPath); err != nil {
				t.Fatal(err)
			}
			meta.PipelineHash, meta.PipelineClass = frozen.Hash, frozen.Class
			policyBefore, err := os.ReadFile(policyPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := state.WriteTaskMeta(h.State, meta); err != nil {
				t.Fatal(err)
			}
			database := filepath.Join(nativeRoot, "state.sqlite")
			runID := "original-run"
			if test.isForeign {
				runID = "foreign-run"
			}
			schema := fmt.Sprintf(`CREATE TABLE repos(id TEXT,working_path TEXT);
CREATE TABLE runs(id TEXT,repo_id TEXT,branch TEXT,status TEXT,head_sha TEXT,last_pushed_sha TEXT,intent TEXT,worktree_dir TEXT,created_at INTEGER);
CREATE TABLE step_results(run_id TEXT,step_name TEXT,round INTEGER,spent INTEGER);
CREATE TABLE findings(run_id TEXT,id TEXT,resolution TEXT);
INSERT INTO repos VALUES('repo-1','%s');
INSERT INTO runs VALUES('%s','repo-1','fix/source-resume','%s','%s','%s','retain validation history','',1);
INSERT INTO step_results VALUES('original-run','review',3,3);
INSERT INTO findings VALUES('original-run','ci-1','unresolved');`, strings.ReplaceAll(filepath.ToSlash(h.Root), "'", "''"), runID, test.status, head, head)
			result, err := commands.Run(t.Context(), execx.Request{Name: "sqlite3", Args: []string{database, schema}})
			if err != nil || result.ExitCode != 0 {
				t.Fatalf("create gate fixture: %v %s", err, result.Stderr)
			}
			before, err := os.ReadFile(database)
			if err != nil {
				t.Fatal(err)
			}
			launches := 0
			var resumed spawn.SwitchRequest
			runtime := commandRuntime{resolveHome: func() (home.Home, error) { return h, nil }, switchTask: func(_ context.Context, _ home.Home, request spawn.SwitchRequest) (spawn.SwitchResult, error) {
				if test.isProviderRefused {
					return spawn.SwitchResult{}, errors.New("provider refused this session")
				}
				launches++
				resumed = request
				return spawn.SwitchResult{}, nil
			}}
			service := lifecycle.Service{StateDir: h.State, Operations: lifecycle.Operations{
				Prepare: func(_ context.Context, _ state.TaskMeta, handoff string) error {
					return os.WriteFile(handoff, []byte("keep the author repair and original validation"), 0o600)
				},
				Stop: func(context.Context, state.TaskMeta, *state.Lifecycle) ([]string, error) { return nil, nil },
				Checkpoint: func(_ context.Context, _ state.TaskMeta, record *state.Lifecycle) error {
					record.GateRun, record.GateIntent, record.GateHead = "original-run", "retain validation history", head
					return nil
				},
				Memory: func() (uint64, uint64, error) {
					if test.isLowMemory {
						return 4 << 30, 5 << 30, nil
					}
					return 5 << 30, 5 << 30, nil
				},
				Admit: func() error {
					if test.isAllowanceDenied {
						return errors.New("provider allowance exhausted")
					}
					return nil
				},
				Resume: func(ctx context.Context, meta state.TaskMeta, prior state.Lifecycle) error {
					return resumeTask(ctx, h, runtime, commands, pipeline.Reader{Root: nativeRoot, Commands: commands}, meta, prior, nil)
				},
				Notify: func(state.Lifecycle) error { return nil },
			}}
			runtime.taskLifecycle = func(ctx context.Context, _ home.Home, request lifecycle.Request, _ string) (state.Lifecycle, error) {
				return service.Run(ctx, request)
			}
			var output, failure bytes.Buffer
			if code := runWithRuntime([]string{"pause", "task", "--operation", "pause-source", "--reason", "overlord"}, &output, &failure, runtime); code != 0 {
				t.Fatalf("public pause exit %d: %s", code, &failure)
			}
			paused, err := state.ReadLifecycle(h.State, meta.ID)
			if err != nil || !paused.HandoffSaved {
				t.Fatalf("pause = %+v, %v", paused, err)
			}
			if test.isUnreadable {
				commands.queryError = errors.New("fixture database locked")
			}

			// Act
			for attempt := 0; attempt < 2; attempt++ {
				output.Reset()
				failure.Reset()
				code := runWithRuntime([]string{"resume", "task", "--operation", "resume-source"}, &output, &failure, runtime)
				if test.refusal != "" {
					if code != 1 || !strings.Contains(failure.String(), test.refusal) {
						t.Fatalf("resume exit %d: %s, want %q", code, &failure, test.refusal)
					}
					break
				}
				if code != 0 {
					t.Fatalf("public resume exit %d: %s; native calls = %d", code, &failure, len(commands.native))
				}
			}

			// Assert
			after, err := os.ReadFile(database)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("original run, rounds, spent repairs or findings changed: %v", err)
			}
			policyAfter, err := os.ReadFile(policyPath)
			if err != nil || !bytes.Equal(policyBefore, policyAfter) {
				t.Fatalf("frozen high-risk policy changed: %v", err)
			}
			if git("rev-parse", "HEAD") != head {
				t.Fatal("resume moved the author's source")
			}
			if content, err := os.ReadFile(unfinished); err != nil || string(content) != "author repair in progress" {
				t.Fatalf("unfinished repair = %q, %v", content, err)
			}
			if test.refusal != "" {
				current, err := state.ReadLifecycle(h.State, meta.ID)
				if err != nil || !reflect.DeepEqual(current, paused) || launches != 0 || len(commands.native) != 0 {
					t.Fatalf("refusal changed pause or validation: %+v, %v, launches = %d, native calls = %d", current, err, launches, len(commands.native))
				}
				return
			}
			if len(commands.native) != 0 || launches != 1 || resumed.ResumeHandoff != paused.Handoff || !resumed.ForceDirty {
				t.Fatalf("resume = %+v, launches = %d, native calls = %d", resumed, launches, len(commands.native))
			}
			if strings.Count(resumed.ResumeNote, "original-run") != 1 {
				t.Fatalf("original validation context must be named once: %q", resumed.ResumeNote)
			}
			current, err := state.ReadLifecycle(h.State, meta.ID)
			if err != nil || current.GateRun != paused.GateRun || current.GateHead != head || current.Phase != "running" {
				t.Fatalf("retained lifecycle = %+v, %v", current, err)
			}
		})
	}
}
