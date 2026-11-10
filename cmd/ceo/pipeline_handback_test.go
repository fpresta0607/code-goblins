package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/standin"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

type handbackCommands struct {
	worktree, branch, callerHead string
	rows                         string
	native                       []execx.Request
	expectation                  map[string]json.RawMessage
	expectationPath              string
	isDirty                      bool
	nativeExit                   int
	shouldWait                   bool
	nativeRefusal                string
}

func (commands *handbackCommands) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	switch request.Name {
	case "git":
		switch strings.Join(request.Args, " ") {
		case "rev-parse --show-toplevel":
			return execx.Result{Stdout: []byte(request.Dir)}, nil
		case "symbolic-ref --quiet --short HEAD":
			if !fsx.SamePath(request.Dir, commands.worktree) {
				return execx.Result{Stdout: []byte("feat/panel-row")}, nil
			}
			return execx.Result{Stdout: []byte(commands.branch)}, nil
		case "rev-parse HEAD":
			return execx.Result{Stdout: []byte(commands.callerHead)}, nil
		case "status --porcelain --untracked-files=all":
			if commands.isDirty {
				return execx.Result{Stdout: []byte(" M repair.go")}, nil
			}
			return execx.Result{}, nil
		}
	case "sqlite3":
		if strings.Contains(request.Args[len(request.Args)-1], "SELECT runs.branch FROM runs") {
			return execx.Result{Stdout: []byte(`[{"branch":"` + commands.branch + `"}]`)}, nil
		}
		return execx.Result{Stdout: []byte(commands.rows)}, nil
	case "no-mistakes":
		commands.native = append(commands.native, request)
		for index, argument := range request.Args {
			if argument == "--expect" && index+1 < len(request.Args) {
				commands.expectationPath = request.Args[index+1]
				data, err := os.ReadFile(commands.expectationPath)
				if err != nil {
					return execx.Result{}, err
				}
				if err := json.Unmarshal(data, &commands.expectation); err != nil {
					return execx.Result{}, err
				}
			}
		}
		if commands.expectation == nil {
			return execx.Result{}, fmt.Errorf("native handback requires the exact expectation file")
		}
		if commands.shouldWait {
			return execx.Result{ExitCode: 1, Stderr: []byte("elapsed while driving the run")}, nil
		}
		if commands.nativeExit != 0 {
			return execx.Result{ExitCode: commands.nativeExit, Stderr: []byte(commands.nativeRefusal)}, nil
		}
		return execx.Result{ExitCode: commands.nativeExit, Stdout: []byte("handback: released\nreceipt:\n  id: native-receipt\n")}, nil
	}
	return execx.Result{}, fmt.Errorf("unexpected handback command: %s %v", request.Name, request.Args)
}

func TestPipelineHandbackRefusesInvalidOrStaleEvidenceWithoutRetry(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "fixture-github-credential")
	for _, test := range []struct {
		name, row, caller, refusal string
		args                       []string
		isDirty                    bool
		shouldWait                 bool
		nativeExit, nativeCalls    int
	}{
		{name: "missing run", row: "[]", refusal: "exactly one latest published run"},
		{name: "ambiguous owner", row: "duplicate", refusal: "exactly one latest published run"},
		{name: "different run", row: "wrong-run", args: []string{"--run", "original-run"}, refusal: "no longer the latest run"},
		{name: "different branch", row: "wrong-branch", refusal: "publication identity"},
		{name: "missing publication", row: "unpublished", refusal: "publication identity"},
		{name: "changed managed head", row: "moved-head", refusal: "exact published managed head"},
		{name: "missing push generation", row: "no-generation", refusal: "publication identity"},
		{name: "dirty caller", isDirty: true, refusal: "clean caller worktree"},
		{name: "foreign receipt caller", caller: "foreign", args: []string{"--resume", "--receipt", "native-receipt"}, refusal: "another caller worktree"},
		{name: "unknown receipt", row: "[]", args: []string{"--resume", "--receipt", "unknown-receipt"}, refusal: "exactly one latest published run"},
		{name: "resume without receipt", args: []string{"--resume"}, refusal: "--resume requires --receipt"},
		{name: "receipt without resume", args: []string{"--receipt", "native-receipt"}, refusal: "--receipt requires --resume"},
		{name: "invalid receipt", args: []string{"--resume", "--receipt", "../foreign"}, refusal: "invalid native handback receipt"},
		{name: "native stale or unsupported request", nativeExit: 1, nativeCalls: 1, refusal: "native handback exited 1"},
		{name: "native continuation hold elapsed", shouldWait: true, nativeCalls: 1, args: []string{"--resume", "--receipt", "native-receipt"}, refusal: "same run continues"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			h, root, branches := extraWorktreeTask(t)
			// Version 6 lets any caller resume, so each refusal is the
			// evidence's own.
			atVersionSix(t, h, root, "task")
			worktree := filepath.Join(h.Root, "worktrees", "project", "task-connections")
			caller, err := fsx.Canonical(worktree)
			if err != nil {
				t.Fatal(err)
			}
			if test.caller == "foreign" {
				caller = t.TempDir()
			}
			commands := &handbackCommands{worktree: worktree, branch: branches[worktree], callerHead: strings.Repeat("b", 40), isDirty: test.isDirty, nativeExit: test.nativeExit, shouldWait: test.shouldWait}
			row := map[string]json.RawMessage{"repo_id": json.RawMessage(`"repo-1"`), "run_id": json.RawMessage(`"original-run"`), "branch": json.RawMessage(strconv.Quote(commands.branch)), "caller_dir": json.RawMessage(strconv.Quote(caller)), "current_head": json.RawMessage(strconv.Quote(strings.Repeat("a", 40))), "published_head": json.RawMessage(strconv.Quote(strings.Repeat("a", 40))), "push_generation": json.RawMessage(`7`)}
			switch test.row {
			case "wrong-run":
				row["run_id"] = json.RawMessage(`"foreign-run"`)
			case "wrong-branch":
				row["branch"] = json.RawMessage(`"fix/foreign"`)
			case "unpublished":
				row["published_head"] = json.RawMessage(`""`)
			case "moved-head":
				row["current_head"] = json.RawMessage(strconv.Quote(strings.Repeat("c", 40)))
			case "no-generation":
				row["push_generation"] = json.RawMessage(`0`)
			}
			rows := []map[string]json.RawMessage{row}
			if test.row == "duplicate" {
				rows = append(rows, row)
			}
			if test.row == "[]" {
				rows = nil
			}
			data, err := json.Marshal(rows)
			if err != nil {
				t.Fatal(err)
			}
			commands.rows = string(data)
			args := append([]string{"handback", "task"}, test.args...)
			if len(test.args) == 0 || test.args[0] != "--run" {
				args = append(args, "--branch", commands.branch)
			}
			var output bytes.Buffer

			// Act
			err = pipelineCommand(t.Context(), h, root, commands, args, &output)

			// Assert
			if err == nil || !strings.Contains(err.Error(), test.refusal) || len(commands.native) != test.nativeCalls {
				t.Fatalf("handback = %v, native calls = %d, want %q and %d calls", err, len(commands.native), test.refusal, test.nativeCalls)
			}
			if test.shouldWait && !errors.Is(err, pipeline.ErrUnresolved) {
				t.Fatalf("elapsed hold = %v, want unresolved work rather than failed validation", err)
			}
			if commands.expectationPath != "" {
				if _, err := os.Stat(commands.expectationPath); !os.IsNotExist(err) {
					t.Fatalf("refused expectation file was not removed: %v", err)
				}
			}
		})
	}
}

func TestPipelineHandbackRefusesMigrationWithoutChangingPersistedContracts(t *testing.T) {
	for _, shouldResume := range []bool{false, true} {
		for _, caller := range []string{"stale run", "dirty worktree"} {
			for _, stage := range []string{"pending", "snapshot", "metadata", "audit", "rollback", "invalid", "unreadable"} {
				t.Run(fmt.Sprintf("resume=%t/%s/%s", shouldResume, caller, stage), func(t *testing.T) {
					h, root, branches := extraWorktreeTask(t)
					meta, err := state.ReadTaskMeta(h.State, "task")
					if err != nil {
						t.Fatal(err)
					}
					current, err := pipeline.LoadSelection(filepath.Join(meta.TaskTmp, "pipeline.json"))
					if err != nil {
						t.Fatal(err)
					}
					old := legacyPipelineSelection(t, current.Class)
					next, err := pipeline.MigrateSelection(old, current.Policy)
					if err != nil {
						t.Fatal(err)
					}
					policyPath := filepath.Join(meta.TaskTmp, "pipeline.json")
					if err := old.Save(policyPath); err != nil {
						t.Fatal(err)
					}
					meta.PipelineHash = old.Hash
					if stage == "snapshot" || stage == "metadata" || stage == "audit" {
						if err := next.Save(policyPath); err != nil {
							t.Fatal(err)
						}
					}
					if stage == "metadata" || stage == "audit" {
						meta.PipelineHash = next.Hash
					}
					if err := state.WriteTaskMeta(h.State, meta); err != nil {
						t.Fatal(err)
					}
					journal := policyMigrationJournal{Version: 1, TaskID: meta.ID, Direction: "forward", Old: old, New: next, Audit: pipelineMigrationAudit(old, next)}
					if stage == "rollback" {
						journal.Direction = "rollback"
					}
					journalPath := filepath.Join(meta.TaskTmp, policyMigrationJournalName)
					if stage == "unreadable" {
						if err := os.Mkdir(journalPath, 0o700); err != nil {
							t.Fatal(err)
						}
					} else if stage == "invalid" {
						if err := os.WriteFile(journalPath, []byte("{"), 0o600); err != nil {
							t.Fatal(err)
						}
					} else if err := writePolicyMigrationJournal(journalPath, journal); err != nil {
						t.Fatal(err)
					}
					if err := state.AppendStatus(h.State, meta.ID, "retained task audit"); err != nil {
						t.Fatal(err)
					}
					if stage == "audit" {
						if err := state.AppendStatus(h.State, meta.ID, journal.Audit); err != nil {
							t.Fatal(err)
						}
					}
					paths := []string{policyPath, filepath.Join(h.State, meta.ID+".meta"), state.StatusPath(h.State, meta.ID)}
					if stage != "unreadable" {
						paths = append(paths, journalPath)
					}
					before := make(map[string][]byte)
					for _, path := range paths {
						before[path], err = os.ReadFile(path)
						if err != nil {
							t.Fatal(err)
						}
					}
					worktree := meta.Extras[0]
					commands := &handbackCommands{worktree: worktree, branch: branches[worktree], callerHead: strings.Repeat("b", 40), isDirty: caller == "dirty worktree"}
					commands.rows = fmt.Sprintf(`[{"repo_id":"repo-1","run_id":"original-run","branch":%q,"current_head":%q,"published_head":%q,"push_generation":7}]`, commands.branch, strings.Repeat("a", 40), strings.Repeat("a", 40))
					args := []string{"handback", meta.ID, "--run", "stale-run"}
					if commands.isDirty {
						args = []string{"handback", meta.ID, "--branch", commands.branch}
						canonical, err := fsx.Canonical(worktree)
						if err != nil {
							t.Fatal(err)
						}
						commands.rows = fmt.Sprintf(`[{"repo_id":"repo-1","run_id":"original-run","branch":%q,"caller_dir":%q,"current_head":%q,"published_head":%q,"push_generation":7}]`, commands.branch, canonical, strings.Repeat("a", 40), strings.Repeat("a", 40))
					}
					if shouldResume {
						args = append(args, "--resume", "--receipt", "native-receipt")
					}

					err = pipelineCommand(t.Context(), h, root, commands, args, &bytes.Buffer{})

					if err == nil || !strings.Contains(err.Error(), "policy migration") || len(commands.native) != 0 {
						t.Errorf("handback = %v, native calls = %d", err, len(commands.native))
					}
					for _, path := range paths {
						after, err := os.ReadFile(path)
						if err != nil || !bytes.Equal(before[path], after) {
							t.Errorf("refused handback changed persisted contract %s: %v", filepath.Base(path), err)
						}
					}
					if stage == "unreadable" {
						entries, err := os.ReadDir(journalPath)
						if err != nil || len(entries) != 0 {
							t.Fatalf("unreadable journal changed: %v, %v", entries, err)
						}
					}
				})
			}
		}
	}
}

func TestPublicPipelineHandbackUsesTheNativeExecutableWithoutReplacingTheRun(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "fixture-github-credential")
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 is needed for persisted native fixtures")
	}
	// Arrange
	policy := versionSixPolicy(t, pipeline.Reviewer{})
	h, root := gateHome(t, policy, operatorMachineConfig)
	worktree, _ := gatedTask(t, h, "task", policy, claudeGoblin)
	profile := t.TempDir()
	for _, name := range []string{"USERPROFILE", "HOME", "APPDATA", "LOCALAPPDATA"} {
		t.Setenv(name, profile)
	}
	t.Setenv("CEO_HOME", h.Root)
	t.Setenv("CEO_STATE_OVERRIDE", h.State)
	t.Setenv("NM_HOME", root)
	if effective, err := os.UserHomeDir(); err != nil || effective != profile {
		t.Fatalf("effective user home = %q, %v", effective, err)
	}
	commands := execx.OSRunner{}
	git := func(dir string, args ...string) string {
		t.Helper()
		result, err := commands.Run(t.Context(), execx.Request{Dir: dir, Name: "git", Args: args})
		if err != nil || result.ExitCode != 0 {
			t.Fatalf("git %q: %v %s", args, err, result.Stderr)
		}
		return strings.TrimSpace(string(result.Stdout))
	}
	project := filepath.Join(h.Root, "project")
	git(project, "init", "-b", "main")
	git(project, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "commit", "--allow-empty", "-m", "test: retain history")
	git(project, "worktree", "add", "-b", "fix/handback", worktree)
	head := git(worktree, "rev-parse", "HEAD")
	database := filepath.Join(root, "state.sqlite")
	schema := fmt.Sprintf(`CREATE TABLE repos(id TEXT,working_path TEXT);
CREATE TABLE runs(id TEXT,repo_id TEXT,branch TEXT,head_sha TEXT,last_pushed_sha TEXT,push_generation INTEGER,created_at INTEGER);
CREATE TABLE ci_handbacks(id TEXT,run_id TEXT,caller_dir TEXT,released_head TEXT,push_generation INTEGER);
INSERT INTO repos VALUES('repo-1','%s');
INSERT INTO runs VALUES('original-run','repo-1','fix/handback','%s','%s',7,1);
INSERT INTO ci_handbacks VALUES('native-receipt','original-run','%s','%s',7);`, strings.ReplaceAll(filepath.ToSlash(project), "'", "''"), head, head, strings.ReplaceAll(worktree, "'", "''"), head)
	created, err := commands.Run(t.Context(), execx.Request{Name: "sqlite3", Args: []string{database, schema}})
	if err != nil || created.ExitCode != 0 {
		t.Fatalf("create isolated native database: %v %s", err, created.Stderr)
	}
	before, err := os.ReadFile(database)
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	nativeName := "no-mistakes"
	if runtime.GOOS == "windows" {
		nativeName += ".exe"
	}
	standin.Put(t, filepath.Join(bin, nativeName))
	standin.RemoveAtCleanup(t, bin)
	record := filepath.Join(t.TempDir(), "native-commands.txt")
	for _, entry := range standin.Env(record, standin.Rule{Args: "axi handback --expect", Prefix: true, Stdout: "handback: released\nreceipt:\n  id: native-receipt\n"}) {
		name, value, _ := strings.Cut(entry, "=")
		t.Setenv(name, value)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	runtime := commandRuntime{resolveHome: func() (home.Home, error) { return h, nil }}

	// Act
	for _, suffix := range [][]string{nil, {"--resume", "--receipt", "native-receipt"}} {
		var output, failure bytes.Buffer
		args := append([]string{"pipeline", "handback", "task", "--run", "original-run"}, suffix...)
		if code := runWithRuntime(args, &output, &failure, runtime); code != 0 || !strings.Contains(output.String(), "native-receipt") {
			t.Fatalf("public handback exit %d: %s %s", code, &failure, &output)
		}
	}

	// Assert
	after, err := os.ReadFile(database)
	if err != nil || !bytes.Equal(before, after) || git(worktree, "rev-parse", "HEAD") != head {
		t.Fatalf("wrapper changed native state or source: %v", err)
	}
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("native calls = %d, want one explicit release and one explicit continuation", len(lines))
	}
	for _, line := range lines {
		_, path, found := strings.Cut(strings.TrimSpace(line), "axi handback --expect ")
		path = strings.TrimSuffix(path, " --resume --receipt native-receipt")
		if !found {
			t.Fatalf("unexpected native operation: %q", line)
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("public command left its expectation file: %v", err)
		}
	}
}

// Under version 6 any caller hands back, and under version 7 the registered
// CEO resumes a correction with no check that its goblin owns it.
func TestPipelineHandbackBindsTheNativeOperationToTheRecordedTask(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "fixture-github-credential")
	for _, version := range []int{6, 7} {
		for _, shouldResume := range []bool{false, true} {
			for _, selector := range []string{"--branch", "--run"} {
				t.Run(fmt.Sprintf("version=%d/resume=%t/%s", version, shouldResume, selector), func(t *testing.T) {
					testHandbackBinding(t, version, shouldResume, selector)
				})
			}
		}
	}
}

func testHandbackBinding(t *testing.T, version int, shouldResume bool, selector string) {
	// Arrange
	h, root, branches := extraWorktreeTask(t)
	if version == 6 {
		atVersionSix(t, h, root, "task")
	} else {
		if runtime.GOOS != "windows" {
			t.Skip("the primary CEO registration is verified on Windows only")
		}
		outsideGoblinTerminal(t)
		registeredPrimary(t, h, "ceo")
	}
	worktree := filepath.Join(h.Root, "worktrees", "project", "task-connections")
	caller, err := fsx.Canonical(worktree)
	if err != nil {
		t.Fatal(err)
	}
	published := strings.Repeat("a", 40)
	head := published
	if shouldResume {
		head = strings.Repeat("b", 40)
	}
	commands := &handbackCommands{worktree: worktree, branch: branches[worktree], callerHead: head}
	commands.rows = fmt.Sprintf(`[{"repo_id":"repo-1","run_id":"original-run","branch":%q,"caller_dir":%q,"current_head":%q,"published_head":%q,"push_generation":7}]`, commands.branch, caller, published, published)
	args := []string{"handback", "task", selector, commands.branch}
	if selector == "--run" {
		args[3] = "original-run"
	}
	if shouldResume {
		args = append(args, "--resume", "--receipt", "native-receipt")
	}
	var output bytes.Buffer

	// Act
	err = pipelineCommand(t.Context(), h, root, commands, args, &output)

	// Assert
	if err != nil || len(commands.native) != 1 {
		t.Fatalf("handback = %v, native calls = %d", err, len(commands.native))
	}
	request := commands.native[0]
	wantArgs := []string{"axi", "handback", "--expect", commands.expectationPath}
	if shouldResume {
		wantArgs = append(wantArgs, "--resume", "--receipt", "native-receipt")
	}
	if !reflect.DeepEqual(request.Args, wantArgs) || !fsx.SamePath(request.Dir, caller) {
		t.Fatalf("native argv = %q in %s, want %q in %s", request.Args, request.Dir, wantArgs, caller)
	}
	want := map[string]string{"repo_id": "repo-1", "run_id": "original-run", "branch": commands.branch, "caller_dir": caller, "caller_head": head, "current_head": published, "published_head": published, "push_generation": "7"}
	if len(commands.expectation) != len(want) {
		t.Fatalf("expectation = %v, want exactly the eight native identity keys", commands.expectation)
	}
	for key, value := range want {
		encoded := string(commands.expectation[key])
		if key != "push_generation" {
			if err := json.Unmarshal(commands.expectation[key], &encoded); err != nil {
				t.Fatal(err)
			}
		}
		if encoded != value {
			t.Errorf("expectation %s = %s, want %s", key, encoded, value)
		}
	}
	if _, err := os.Stat(commands.expectationPath); !os.IsNotExist(err) {
		t.Fatalf("expectation file was not removed: %v", err)
	}
	if !strings.Contains(output.String(), "native-receipt") {
		t.Fatalf("native receipt was not delivered: %q", &output)
	}
}

func TestPipelineHandbackPublicationRevalidationPreservesNativeBinding(t *testing.T) {
	for _, shouldResume := range []bool{false, true} {
		for _, selector := range []string{"--branch", "--run"} {
			t.Run(fmt.Sprintf("resume=%t/%s", shouldResume, selector), func(t *testing.T) {
				// Arrange
				h, root, fixture := admissionControllerStage(t)
				outsideGoblinTerminal(t)
				caller, err := fsx.Canonical(fixture.worktree)
				if err != nil {
					t.Fatal(err)
				}
				published := strings.Repeat("a", 40)
				head := published
				if shouldResume {
					head = strings.Repeat("b", 40)
				}
				commands := &handbackCommands{worktree: caller, branch: "fix/publication", callerHead: head}
				commands.rows = fmt.Sprintf(`[{"repo_id":"repo-1","run_id":"original-run","branch":%q,"caller_dir":%q,"current_head":%q,"published_head":%q,"push_generation":7}]`, commands.branch, caller, published, published)
				args := []string{"handback", "task", selector, commands.branch, "--revalidate-publication"}
				if selector == "--run" {
					args[3] = "original-run"
				}
				if shouldResume {
					args = append(args, "--resume", "--receipt", "native-receipt")
				}
				meta, err := state.ReadTaskMeta(h.State, "task")
				if err != nil {
					t.Fatal(err)
				}
				policyBefore, err := os.ReadFile(filepath.Join(meta.TaskTmp, "pipeline.json"))
				if err != nil {
					t.Fatal(err)
				}
				var output bytes.Buffer

				// Act
				err = pipelineCommand(t.Context(), h, root, commands, args, &output)

				// Assert
				if err != nil || len(commands.native) != 1 {
					t.Fatalf("handback = %v, native calls = %d", err, len(commands.native))
				}
				wantArgs := []string{"axi", "handback", "--expect", commands.expectationPath, "--revalidate-publication"}
				if shouldResume {
					wantArgs = append(wantArgs, "--resume", "--receipt", "native-receipt")
				}
				if !reflect.DeepEqual(commands.native[0].Args, wantArgs) || !fsx.SamePath(commands.native[0].Dir, caller) {
					t.Fatalf("native request = %+v, want %q in %s", commands.native[0], wantArgs, caller)
				}
				want := fmt.Sprintf(`{"repo_id":"repo-1","run_id":"original-run","branch":%q,"caller_dir":%q,"caller_head":%q,"current_head":%q,"published_head":%q,"push_generation":7}`, commands.branch, caller, head, published, published)
				var expected map[string]json.RawMessage
				if err := json.Unmarshal([]byte(want), &expected); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(commands.expectation, expected) {
					t.Fatalf("native binding = %v, want %v", commands.expectation, expected)
				}
				policyAfter, err := os.ReadFile(filepath.Join(meta.TaskTmp, "pipeline.json"))
				if err != nil || !bytes.Equal(policyBefore, policyAfter) {
					t.Fatalf("handback changed frozen policy: %v", err)
				}
				if _, err := os.Stat(commands.expectationPath); !os.IsNotExist(err) {
					t.Fatalf("expectation file was not removed: %v", err)
				}
				if !strings.Contains(output.String(), "native-receipt") {
					t.Fatalf("native receipt was not delivered: %q", &output)
				}
			})
		}
	}
}

func TestPipelineHandbackPublicationRevalidationRequiresPrimaryCEO(t *testing.T) {
	for _, scenario := range []struct {
		name, terminal, gate string
		isInvalidProof       bool
	}{
		{name: "gate inherits CEO proof", gate: "1"},
		{name: "own contributor inherits CEO ancestry", terminal: "task"},
		{name: "foreign contributor inherits CEO ancestry", terminal: "another-task"},
		{name: "invalid contributor proof inherits CEO ancestry", terminal: "task", isInvalidProof: true},
		{name: "gate inherits own contributor proof", terminal: "task", gate: "1"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Arrange
			h, root, fixture := admissionControllerStage(t)
			inGoblinTerminal(t, h.State, "ceo")
			t.Setenv(harness.RoleVariable, "ceo")
			record, err := host.ReadRecord(h.State, "ceo")
			if err != nil {
				t.Fatal(err)
			}
			var isStarted bool
			record.ChildStart, isStarted = proc.StartTime(os.Getpid())
			if !isStarted {
				t.Fatal("the registered CEO process has no start time")
			}
			record.ProofSum = host.ProofSum(os.Getenv(host.ProofVariable))
			data, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(h.State, "hosts", "ceo.json"), data, 0o600); err != nil {
				t.Fatal(err)
			}
			if scenario.terminal != "" {
				inGoblinTerminal(t, h.State, scenario.terminal)
			}
			if scenario.isInvalidProof {
				t.Setenv(host.ProofVariable, "unissued-proof")
			}
			t.Setenv(gateAgentVariable, scenario.gate)
			connection := supervisor.CEOConnection{State: h.State}
			_, release, err := connection.CallerIdentity()
			if err != nil {
				t.Fatalf("fixture must inherit CEO identity: %v", err)
			}
			release()
			commands := &handbackCommands{worktree: fixture.worktree, branch: "fix/publication"}
			var output bytes.Buffer

			// Act
			err = pipelineCommand(t.Context(), h, root, commands, []string{"handback", "task", "--revalidate-publication"}, &output)

			// Assert
			if err == nil || !strings.Contains(err.Error(), "registered primary CEO") || len(commands.native) != 0 || output.Len() != 0 {
				t.Fatalf("unauthorized handback = %v, native calls = %d, output = %q", err, len(commands.native), &output)
			}
		})
	}
}

func TestPipelineHandbackReportsNativePublicationRefusalWithoutClaimingMissingExpectation(t *testing.T) {
	for _, test := range []struct {
		refusal                     string
		shouldRevalidatePublication bool
	}{
		{refusal: "ordinary CI handback requires exact reviewed publication and request identity with automatic repair disabled"},
		{refusal: "publication revalidation requires submitted, reviewed, tested and published ancestry", shouldRevalidatePublication: true},
	} {
		t.Run(test.refusal, func(t *testing.T) {
			// Arrange
			h, root, fixture := admissionControllerStage(t)
			outsideGoblinTerminal(t)
			published := strings.Repeat("a", 40)
			commands := &handbackCommands{worktree: fixture.worktree, branch: "fix/publication", callerHead: published, nativeExit: 1, nativeRefusal: test.refusal}
			commands.rows = fmt.Sprintf(`[{"repo_id":"repo-1","run_id":"original-run","branch":%q,"current_head":%q,"published_head":%q,"push_generation":7}]`, commands.branch, published, published)
			args := []string{"handback", "task", "--branch", commands.branch}
			if test.shouldRevalidatePublication {
				args = append(args, "--revalidate-publication")
			}
			var output bytes.Buffer

			// Act
			err := pipelineCommand(t.Context(), h, root, commands, args, &output)

			// Assert
			if err == nil || strings.Contains(err.Error(), "--expect") || output.String() != test.refusal || len(commands.native) != 1 {
				t.Fatalf("native refusal = %v, output = %q, native calls = %d", err, &output, len(commands.native))
			}
		})
	}
}
