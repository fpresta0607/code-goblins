package pipeline

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

type interruptionRunner struct {
	run          InterruptedRun
	commands     []string
	failMerge    bool
	pins         map[string]string
	gateHead     string
	gateDirty    bool
	editsCommit  string
	committedFor string
	abortEnv     []string
}

func (runner *interruptionRunner) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	command := request.Name + " " + strings.Join(request.Args, " ")
	runner.commands = append(runner.commands, command)
	if strings.Contains(command, "axi abort --run") {
		runner.run.Status = "cancelled"
		runner.abortEnv = request.Env
	}
	if request.Name == "sqlite3" {
		data, _ := json.Marshal([]InterruptedRun{runner.run})
		return execx.Result{Stdout: data}, nil
	}
	if strings.Contains(command, "symbolic-ref --quiet") {
		return execx.Result{ExitCode: 1}, nil
	}
	if strings.Contains(command, "show-ref --exists") {
		if runner.pins[request.Args[len(request.Args)-1]] == "" {
			return execx.Result{ExitCode: 2}, nil
		}
		return execx.Result{}, nil
	}
	if strings.Contains(command, "show-ref --verify") {
		head := runner.pins[request.Args[len(request.Args)-1]]
		if head == "" {
			return execx.Result{ExitCode: 128}, nil
		}
		return execx.Result{Stdout: []byte(head)}, nil
	}
	if strings.Contains(command, "update-ref --no-deref") {
		if runner.pins == nil {
			runner.pins = map[string]string{}
		}
		for i, argument := range request.Args {
			if argument == "--no-deref" {
				runner.pins[request.Args[i+1]] = request.Args[i+2]
			}
		}
	}
	if strings.Contains(command, "cat-file -t") {
		return execx.Result{Stdout: []byte("commit")}, nil
	}
	if strings.Contains(command, "symbolic-ref --short") {
		return execx.Result{Stdout: []byte("feat/task\n")}, nil
	}
	if strings.Contains(command, "merge --no-edit") && runner.failMerge {
		return execx.Result{ExitCode: 1}, nil
	}
	if strings.Contains(command, "rev-parse HEAD") {
		if request.Dir == runner.run.Worktree && runner.gateHead != "" {
			return execx.Result{Stdout: []byte(runner.gateHead)}, nil
		}
		return execx.Result{Stdout: []byte(runner.run.Head)}, nil
	}
	if strings.Contains(command, "^{tree}") {
		return execx.Result{Stdout: []byte(strings.Repeat("d", 40))}, nil
	}
	if strings.Contains(command, "write-tree") {
		if runner.gateDirty {
			return execx.Result{Stdout: []byte(strings.Repeat("e", 40))}, nil
		}
		return execx.Result{Stdout: []byte(strings.Repeat("d", 40))}, nil
	}
	if strings.Contains(command, "commit-tree") {
		runner.committedFor = request.Args[len(request.Args)-1]
		if runner.editsCommit == "" {
			runner.editsCommit = strings.Repeat("f", 40)
		}
		return execx.Result{Stdout: []byte(runner.editsCommit)}, nil
	}
	if strings.Contains(command, "rev-parse --show-toplevel") {
		return execx.Result{Stdout: []byte(request.Dir)}, nil
	}
	if strings.Contains(command, "rev-parse --path-format=absolute --git-common-dir") {
		root := filepath.Dir(filepath.Dir(filepath.Dir(runner.run.Worktree)))
		return execx.Result{Stdout: []byte(filepath.Join(root, "repos", runner.run.RepoID+".git"))}, nil
	}
	if strings.Contains(command, "status --porcelain") && runner.gateDirty {
		return execx.Result{Stdout: []byte(" M unfinished.go\n?? new-test.go\n")}, nil
	}
	return execx.Result{}, nil
}

// A gate's run worktree is verified against a folder the fleet computed, and
// git answers with the name the operating system gives the folder it runs in.
// On a CI runner's temporary folder that is the 8.3 short name of the very
// same folder, so comparing the two as text stopped recovery before the
// fixer's edits were kept (PR 1516).
func TestSameFolderSeesThroughAnotherSpellingOfOneFolder(t *testing.T) {
	// Arrange
	folder := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(folder, alias); err != nil {
		// Windows without Developer Mode and without the right to make
		// symbolic links falls back to a junction, which needs neither.
		if out, junctionErr := exec.Command("cmd", "/c", "mklink", "/J", alias, folder).CombinedOutput(); junctionErr != nil {
			t.Skipf("no alias can be made here: %v (%s)", junctionErr, out)
		}
	}

	// Act & Assert
	if !sameFolder(alias, folder) || !sameFolder(folder, alias) {
		t.Errorf("sameFolder(%q, %q) = false, want one folder under two names", alias, folder)
	}
	if sameFolder(alias, t.TempDir()) {
		t.Error("two different folders were read as one")
	}
}

func TestInterruptPreservesTheGateWorktreeBeyondItsRecordedHead(t *testing.T) {
	for _, isDirty := range []bool{false, true} {
		t.Run(map[bool]string{false: "unrecorded commit", true: "unfinished changes"}[isDirty], func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "state.sqlite"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			run := InterruptedRun{ID: "run-1", RepoID: "repo", Branch: "feat/task", Status: "running", Head: strings.Repeat("a", 40), Worktree: filepath.Join(root, "worktrees", "repo", "run-1")}
			if err := os.MkdirAll(run.Worktree, 0o700); err != nil {
				t.Fatal(err)
			}
			runner := &interruptionRunner{run: run, gateHead: strings.Repeat("b", 40), gateDirty: isDirty}
			interrupted, err := (Reader{Root: root, Commands: runner}).Interrupt(context.Background(), "project", t.TempDir(), run.Branch, run.ID)
			if err != nil {
				t.Fatalf("interruption=%v", err)
			}
			commands := strings.Join(runner.commands, "\n")
			pin := strings.Index(commands, "update-ref --no-deref refs/heads/archive/pause-run-1-"+runner.gateHead)
			abort := strings.Index(commands, "axi abort --run")
			if pin < 0 || abort < pin {
				t.Fatalf("gate work was not preserved before abort:\n%s", commands)
			}
			// The abort removes the run's worktree with force, so the edits it
			// holds are committed and pinned under the run before it runs.
			edits := strings.Index(commands, "update-ref --no-deref "+WorktreeRef(run.ID)+" "+runner.editsCommit)
			if !isDirty {
				if edits >= 0 || interrupted.WorktreeEdits != "" {
					t.Fatalf("a clean worktree was preserved as edits:\n%s", commands)
				}
				return
			}
			if edits < 0 || edits > abort {
				t.Fatalf("unfinished gate changes were not pinned before the abort:\n%s", commands)
			}
			if interrupted.WorktreeEdits != runner.editsCommit {
				t.Fatalf("worktree edits = %q, want the pinned %q", interrupted.WorktreeEdits, runner.editsCommit)
			}
		})
	}
}

func TestResumeInterruptedRetainsTheExactRunWithoutMutation(t *testing.T) {
	for _, status := range []string{"cancelled", "failed", "completed", "ci_monitor_interrupted", "running", "pending"} {
		for _, test := range []struct {
			name, pushed string
		}{
			{"published its head", strings.Repeat("a", 40)},
			{"moved past its push", strings.Repeat("b", 40)},
			{"never pushed", ""},
		} {
			t.Run(status+"/"+test.name, func(t *testing.T) {
				// Arrange
				root := t.TempDir()
				if err := os.WriteFile(filepath.Join(root, "state.sqlite"), nil, 0o600); err != nil {
					t.Fatal(err)
				}
				prior := InterruptedRun{ID: "original-run", RepoID: "repo", Branch: "feat/task", Status: status, Head: strings.Repeat("a", 40), PushedHead: test.pushed, Intent: "retain history"}
				runner := &interruptionRunner{run: prior}
				reader := Reader{Root: root, Commands: runner}
				for attempt := 0; attempt < 2; attempt++ {
					// Act
					current, err := reader.ResumeInterrupted(t.Context(), "project", prior)
					// Assert
					if err != nil || current != prior {
						t.Fatalf("resume = %+v, %v", current, err)
					}
					if len(runner.commands) != attempt+1 || !strings.HasPrefix(runner.commands[attempt], "sqlite3 -readonly -json ") {
						t.Fatalf("commands = %q, want only one read per resume", runner.commands)
					}
				}
			})
		}
	}
}

func TestResumeInterruptedRefusesChangedIdentity(t *testing.T) {
	for _, test := range []struct{ name, id, intent, want string }{
		{"different run with identical intent", "other-run", "retain history", "another validation run owns"},
		{"different run with different intent", "other-run", "Unrelated work", "another validation run owns"},
		{"same run with changed intent", "original-run", "other work", "saved validation intent changed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "state.sqlite"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			prior := InterruptedRun{ID: "original-run", RepoID: "repo", Branch: "feat/task", Status: "cancelled", Head: strings.Repeat("a", 40), Intent: "retain history"}
			current := prior
			current.ID, current.Status, current.Intent = test.id, "failed", test.intent
			runner := &interruptionRunner{run: current}
			// Act
			_, err := (Reader{Root: root, Commands: runner}).ResumeInterrupted(t.Context(), "project", prior)
			// Assert
			if err == nil || !strings.Contains(err.Error(), test.want) || len(runner.commands) != 1 || !strings.HasPrefix(runner.commands[0], "sqlite3 -readonly -json ") {
				t.Fatalf("resume = %v, commands = %q", err, runner.commands)
			}
		})
	}
}

func TestInterruptPinsAndImportsGateCommitsBeforeAbortingOnlyItsRun(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "preserved", true: "merge refused"}[failed], func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "state.sqlite"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			run := InterruptedRun{ID: "run-1", RepoID: "repo-1", Branch: "feat/task", Status: "running", Head: strings.Repeat("a", 40), Intent: "Keep all gate fixes", Worktree: filepath.Join(root, "worktrees", "repo-1", "run-1")}
			runner := &interruptionRunner{run: run, failMerge: failed}
			reader := Reader{Root: root, Commands: runner}
			_, err := reader.Interrupt(context.Background(), "project", t.TempDir(), "feat/task", run.ID)
			if (err != nil) != failed {
				t.Fatalf("interrupt error=%v", err)
			}
			joined := strings.Join(runner.commands, "\n")
			pin := strings.Index(joined, "update-ref --no-deref refs/heads/archive/pause-run-1-")
			fetch := strings.Index(joined, "fetch --no-tags")
			merge := strings.Index(joined, "merge --no-edit")
			abort := strings.Index(joined, "axi abort --run run-1")
			if pin < 0 || fetch < pin || merge < fetch || !failed && abort < merge || failed && abort >= 0 {
				t.Fatalf("unsafe sequence:\n%s", joined)
			}
			if !failed && !slices.Contains(runner.abortEnv, "NM_HOME="+root) {
				t.Errorf("the abort ran without NM_HOME=%s, so a board started before the gate's home moved aborts in the old home", root)
			}
			for _, forbidden := range []string{"--amend", "reset ", "--force", "abort --all"} {
				if strings.Contains(joined, forbidden) {
					t.Fatalf("unsafe command %q", forbidden)
				}
			}
		})
	}
}

func TestInterruptRejectsAnotherGateRun(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "state.sqlite"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &interruptionRunner{run: InterruptedRun{ID: "replacement", RepoID: "repo", Branch: "feat/task", Head: strings.Repeat("a", 40)}}
	_, err := (Reader{Root: root, Commands: runner}).Interrupt(context.Background(), "project", t.TempDir(), "feat/task", "requested")
	if err == nil || len(runner.commands) != 1 {
		t.Fatalf("replacement gate changed: %v %v", runner.commands, err)
	}
}

func TestTrustedHeadIsTheHeadOfOriginsDefaultBranch(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "state.sqlite"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	const head = "0123456789abcdef0123456789abcdef01234567"
	for _, test := range []struct {
		name, repos, remote string
		want                string
	}{
		{"registered", `[{"default_branch":"main"}]`, "ref: refs/heads/main\tHEAD\n" + head + "\tHEAD\n", head},
		{"not registered", `[]`, "ref: refs/heads/main\tHEAD\n" + head + "\tHEAD\n", ""},
		{"another default branch at origin", `[{"default_branch":"main"}]`, "ref: refs/heads/trunk\tHEAD\n" + head + "\tHEAD\n", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader := Reader{Root: root, Commands: runnerFunc(func(_ context.Context, request execx.Request) (execx.Result, error) {
				if request.Name == "sqlite3" {
					return execx.Result{Stdout: []byte(test.repos)}, nil
				}
				return execx.Result{Stdout: []byte(test.remote)}, nil
			})}
			got, err := reader.TrustedHead(context.Background(), "project")
			if got != test.want || (err == nil) != (test.want != "") {
				t.Fatalf("TrustedHead = %q, %v, want %q", got, err, test.want)
			}
		})
	}
}
