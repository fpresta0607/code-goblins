package pipeline

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

type interruptionRunner struct {
	run       InterruptedRun
	commands  []string
	failMerge bool
	pins      map[string]string
	startRun  bool
	endStatus string
	gateHead  string
	gateDirty bool
}

func (runner *interruptionRunner) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	command := request.Name + " " + strings.Join(request.Args, " ")
	runner.commands = append(runner.commands, command)
	if strings.Contains(command, "axi abort --run") {
		runner.run.Status = "cancelled"
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
			_, err := (Reader{Root: root, Commands: runner}).Interrupt(context.Background(), "project", t.TempDir(), run.Branch, run.ID)
			if (err != nil) != isDirty {
				t.Fatalf("interruption=%v", err)
			}
			commands := strings.Join(runner.commands, "\n")
			pin := strings.Index(commands, "update-ref --no-deref refs/heads/archive/pause-run-1-"+runner.gateHead)
			abort := strings.Index(commands, "axi abort --run")
			if pin < 0 || isDirty && abort >= 0 || !isDirty && abort < pin {
				t.Fatalf("gate work was not preserved before abort:\n%s", commands)
			}
		})
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
			err := reader.RestartInterrupted(context.Background(), "project", t.TempDir(), prior)
			if (err == nil) != starts {
				t.Fatalf("restart: %v", err)
			}
			commands := strings.Join(runner.commands, "\n")
			if strings.Index(commands, "axi sync --recover") > strings.Index(commands, "axi run --intent") {
				t.Fatal("started validation before recovery")
			}
			if starts {
				if err := reader.RestartInterrupted(context.Background(), "project", t.TempDir(), prior); err != nil {
					t.Fatal(err)
				}
				if strings.Count(strings.Join(runner.commands, "\n"), "axi run --intent") != 1 {
					t.Fatal("retry restarted the replacement validation")
				}
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
				if err := reader.RestartInterrupted(context.Background(), "project", t.TempDir(), prior); err != nil {
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
	err := (Reader{Root: root, Commands: runner}).RestartInterrupted(context.Background(), "project", t.TempDir(), prior)
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
