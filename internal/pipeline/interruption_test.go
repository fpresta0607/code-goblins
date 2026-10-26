package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

type interruptionRunner struct {
	run          InterruptedRun
	commands     []string
	failMerge    bool
	pins         map[string]string
	startRun     bool
	endStatus    string
	gateHead     string
	gateDirty    bool
	editsCommit  string
	committedFor string
}

func (runner *interruptionRunner) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	command := request.Name + " " + strings.Join(request.Args, " ")
	runner.commands = append(runner.commands, command)
	if strings.Contains(command, "axi abort --run") {
		runner.run.Status = "cancelled"
	}
	// no-mistakes recovers custody only from a run holding commits it never
	// pushed: one whose head is its push binding is refused, as the gate's
	// own tests of a pushed run's recovery hold.
	if strings.Contains(command, "axi sync --recover") && runner.run.PushedHead != "" && runner.run.PushedHead == runner.run.Head {
		return execx.Result{ExitCode: 1, Stdout: []byte("error: nothing was recovered: the live remote contains commits outside the persisted pipeline push binding; no files or refs were changed")}, nil
	}
	if strings.Contains(command, "axi run --intent") {
		if runner.startRun {
			runner.run.ID, runner.run.Status = "new-run", "running"
			if runner.endStatus != "" {
				runner.run.Status = runner.endStatus
			}
		}
		return execx.Result{ExitCode: 1, Stderr: []byte("bounded wait elapsed")}, nil
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

// A run its goblin's pause could not stop is still live when the goblin
// resumes: the resume goes on, the goblin picks its run back up, and no
// replacement is started beside it. On 2026-10-09 Murray was paused for
// memory with his gate run live, the pause could not abort the run, and each
// of three resumes ended failed with "paused validation has not stopped; no
// replacement run was started" until the CFO switched him by hand.
func TestRestartInterruptedLeavesARunStillLiveToItsGoblin(t *testing.T) {
	// Arrange
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "state.sqlite"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	live := InterruptedRun{ID: "paused-run", RepoID: "repo", Branch: "feat/task", Status: "running", Head: strings.Repeat("a", 40), Intent: "Keep the gate fixes"}
	runner := &interruptionRunner{run: live, startRun: true}
	reader := Reader{Root: root, Commands: runner}

	// Act
	err := reader.RestartInterrupted(context.Background(), "project", t.TempDir(), live, nil)

	// Assert
	if err != nil {
		t.Errorf("restart = %v, want the resume to go on with its run still live", err)
	}
	if commands := strings.Join(runner.commands, "\n"); strings.Contains(commands, "no-mistakes") {
		t.Errorf("commands:\n%s\nwant the live run left alone, with no recovery, replacement or abort", commands)
	}
}

func TestRestartInterruptedChecksAcceptedRunAfterBoundedWaitExpires(t *testing.T) {
	for _, starts := range []bool{true, false} {
		t.Run(map[bool]string{true: "accepted", false: "refused"}[starts], func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "state.sqlite"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			prior := InterruptedRun{ID: "paused-run", RepoID: "repo", Branch: "feat/task", Status: "cancelled", Head: strings.Repeat("a", 40), Intent: "Keep the gate fixes"}
			runner := &interruptionRunner{run: prior, startRun: starts}
			reader := Reader{Root: root, Commands: runner}
			err := reader.RestartInterrupted(context.Background(), "project", t.TempDir(), prior, nil)
			if (err == nil) != starts {
				t.Fatalf("restart: %v", err)
			}
			commands := strings.Join(runner.commands, "\n")
			if strings.Index(commands, "axi sync --recover") > strings.Index(commands, "axi run --intent") {
				t.Fatal("started validation before recovery")
			}
			if starts {
				if err := reader.RestartInterrupted(context.Background(), "project", t.TempDir(), prior, nil); err != nil {
					t.Fatal(err)
				}
				if strings.Count(strings.Join(runner.commands, "\n"), "axi run --intent") != 1 {
					t.Fatal("retry restarted the replacement validation")
				}
			}
		})
	}
}

// A run that published its head holds nothing for custody recovery to
// return, so its replacement starts from the branch as its goblin left it.
// On 2026-10-17 cg-canvas-live-only was paused on the CI of the pull request
// its gate had opened, with its goblin's one follow-up commit on it, and its
// resume was refused with "nothing was recovered: the live remote contains
// commits outside the persisted pipeline push binding".
func TestRestartInterruptedRecoversOnlyARunHoldingUnpublishedCommits(t *testing.T) {
	head := strings.Repeat("a", 40)
	for _, test := range []struct {
		name, pushed  string
		shouldRecover bool
	}{
		{"published its head", head, false},
		{"moved past its push", strings.Repeat("b", 40), true},
		{"never pushed", "", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "state.sqlite"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			prior := InterruptedRun{ID: "paused-run", RepoID: "repo", Branch: "feat/task", Status: "cancelled", Head: head, PushedHead: test.pushed, Intent: "Keep the gate fixes"}
			runner := &interruptionRunner{run: prior, startRun: true}

			// Act
			err := (Reader{Root: root, Commands: runner}).RestartInterrupted(context.Background(), "project", t.TempDir(), prior, nil)

			// Assert
			if err != nil {
				t.Fatalf("restart = %v, want the replacement started", err)
			}
			commands := strings.Join(runner.commands, "\n")
			recovery, start := strings.Index(commands, "axi sync --recover"), strings.Index(commands, "axi run --intent Keep the gate fixes")
			if start < 0 || (recovery >= 0) != test.shouldRecover || recovery > start {
				t.Fatalf("commands:\n%s\nwant recovery %v before the replacement starts", commands, test.shouldRecover)
			}
		})
	}
}

func TestRestartInterruptedAcceptsAReplacementThatAlreadyEnded(t *testing.T) {
	for _, status := range []string{"failed", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "state.sqlite"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			prior := InterruptedRun{ID: "paused-run", RepoID: "repo", Branch: "feat/task", Status: "cancelled", Head: strings.Repeat("a", 40), Intent: "Keep the gate fixes"}
			runner := &interruptionRunner{run: prior, startRun: true, endStatus: status}
			reader := Reader{Root: root, Commands: runner}
			for attempt := 0; attempt < 2; attempt++ {
				if err := reader.RestartInterrupted(context.Background(), "project", t.TempDir(), prior, nil); err != nil {
					t.Fatalf("attempt %d: replacement that %s blocked Resume: %v", attempt, status, err)
				}
			}
			if count := strings.Count(strings.Join(runner.commands, "\n"), "axi run --intent"); count != 1 {
				t.Fatalf("started %d replacement runs", count)
			}
		})
	}
}

func TestRestartInterruptedRefusesARunWithAnotherIntent(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "state.sqlite"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	prior := InterruptedRun{ID: "paused-run", RepoID: "repo", Branch: "feat/task", Status: "cancelled", Head: strings.Repeat("a", 40), Intent: "Keep the gate fixes"}
	other := prior
	other.ID, other.Status, other.Intent = "other-run", "failed", "Unrelated work"
	runner := &interruptionRunner{run: other}
	err := (Reader{Root: root, Commands: runner}).RestartInterrupted(context.Background(), "project", t.TempDir(), prior, nil)
	commands := strings.Join(runner.commands, "\n")
	if err == nil || strings.Contains(commands, "no-mistakes") {
		t.Fatalf("restart over another intent: %v\n%s", err, commands)
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

// A gate that runs on its task's own harness carries that as a launch
// selection, so the run Resume starts in place of a paused one carries it
// too. Without it the replacement ran whatever the machine's chain named.
func TestRestartInterruptedStartsTheReplacementUnderItsLaunchSelection(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "state.sqlite"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	prior := InterruptedRun{ID: "paused-run", RepoID: "repo", Branch: "feat/task", Status: "cancelled", Head: strings.Repeat("a", 40), Intent: "Keep the gate fixes"}
	runner := &interruptionRunner{run: prior, startRun: true}
	selected := []string{"--launch-nonce", "cfo-1", "--validation-generation", "frozen", "--launch-assertion", "launch-selection.json"}

	err := (Reader{Root: root, Commands: runner}).RestartInterrupted(context.Background(), "project", t.TempDir(), prior, func(context.Context) ([]string, error) {
		return selected, nil
	})

	if err != nil {
		t.Fatal(err)
	}
	want := "no-mistakes axi run --intent Keep the gate fixes --wait 45s " + strings.Join(selected, " ")
	if commands := strings.Join(runner.commands, "\n"); !strings.Contains(commands+"\n", want+"\n") {
		t.Fatalf("commands:\n%s\nwant the replacement started as %q", commands, want)
	}
}

func TestRestartInterruptedAsksForNoLaunchSelectionItWillNotUse(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "state.sqlite"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	live := InterruptedRun{ID: "paused-run", RepoID: "repo", Branch: "feat/task", Status: "running", Head: strings.Repeat("a", 40), Intent: "Keep the gate fixes"}
	runner := &interruptionRunner{run: live}

	err := (Reader{Root: root, Commands: runner}).RestartInterrupted(context.Background(), "project", t.TempDir(), live, func(context.Context) ([]string, error) {
		t.Error("a run still live was given a launch selection")
		return nil, nil
	})

	if err != nil {
		t.Fatal(err)
	}
}

func TestRestartInterruptedStartsNothingWithoutItsLaunchSelection(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "state.sqlite"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	prior := InterruptedRun{ID: "paused-run", RepoID: "repo", Branch: "feat/task", Status: "cancelled", Head: strings.Repeat("a", 40), Intent: "Keep the gate fixes"}
	runner := &interruptionRunner{run: prior, startRun: true}

	err := (Reader{Root: root, Commands: runner}).RestartInterrupted(context.Background(), "project", t.TempDir(), prior, func(context.Context) ([]string, error) {
		return nil, errors.New("no gate agent can be proved")
	})

	if err == nil || !strings.Contains(err.Error(), "no gate agent can be proved") {
		t.Fatalf("error = %v, want the refused launch selection", err)
	}
	if commands := strings.Join(runner.commands, "\n"); strings.Contains(commands, "no-mistakes") {
		t.Fatalf("commands:\n%s\nwant nothing recovered or started on the machine's own chain", commands)
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
