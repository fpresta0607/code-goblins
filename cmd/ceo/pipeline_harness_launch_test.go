package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// The operator names a fallback after a task froze version 6 without one, and
// the task takes it through ceo pipeline migrate, journal and all.
func TestPipelineMigrateGivesAVersionSixTaskTheFallbackNamedSince(t *testing.T) {
	frozen := versionSixPolicy(t, pipeline.Reviewer{})
	named := versionSixPolicy(t, codexGoblin)
	h, nm := gateHome(t, frozen, operatorMachineConfig)
	wt, old := gatedTask(t, h, "task", frozen, claudeGoblin)
	current, err := json.Marshal(named)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(h.Root, "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.Root, "config", "pipeline.json"), current, 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &gateStartRunner{pipelineStartRunner: pipelineStartRunner{worktree: wt}}

	if err := pipelineCommand(context.Background(), h, nm, runner, []string{"migrate", "task"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	migrated, err := pipeline.LoadSelection(filepath.Join(h.State, "tasktmp", "task", "pipeline.json"))
	if err != nil || migrated.Policy != named || migrated.Hash == old.Hash || migrated.Class != old.Class || migrated.ReviewCycles != old.ReviewCycles {
		t.Fatalf("snapshot after migrate = %+v, %v, want the named fallback with the class and cap kept", migrated, err)
	}
	meta, err := state.ReadTaskMeta(h.State, "task")
	if err != nil || meta.PipelineHash != migrated.Hash {
		t.Fatalf("task record hash = %q, %v, want %q", meta.PipelineHash, err, migrated.Hash)
	}
	if len(runner.native) != 0 {
		t.Fatalf("a migration started a gate: %+v", runner.native)
	}
}

// Two starts of one task never share a selection file, so a later start
// cannot replace the file an earlier no-mistakes is about to read.
func TestEachLaunchWritesItsOwnSelectionFile(t *testing.T) {
	policy := versionSixPolicy(t, pipeline.Reviewer{})
	h, nm := gateHome(t, policy, operatorMachineConfig)
	wt, _ := gatedTask(t, h, "task", policy, claudeGoblin)
	runner := &gateStartRunner{pipelineStartRunner: pipelineStartRunner{worktree: wt}}

	for range 2 {
		if err := pipelineCommand(context.Background(), h, nm, runner, []string{"run", "task", "--intent", "ship safely"}, &bytes.Buffer{}); err != nil {
			t.Fatalf("pipelineCommand: %v", err)
		}
	}

	if len(runner.native) != 2 {
		t.Fatalf("native launches=%+v, want two", runner.native)
	}
	first, second := runner.native[0].Args[9], runner.native[1].Args[9]
	if first == second {
		t.Fatalf("both launches named %s", first)
	}
	for _, path := range []string{first, second} {
		if filepath.Dir(path) != filepath.Join(h.State, "tasktmp", "task") {
			t.Fatalf("selection file %s is outside the task's own folder", path)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("selection file: %v", err)
		}
	}
}

func TestPipelineRunTellsABuildWithoutLaunchSelectionsFromARefusedSelection(t *testing.T) {
	for _, test := range []struct {
		name, said string
		isBuild    bool
	}{
		{"no strict launch at all", "Error: unknown flag: --launch-nonce\n", true},
		{"no launch assertion", "Error: unknown flag: --launch-assertion\n", true},
		{"proves but cannot apply", "error: unreadable launch assertion\n", true},
		{"a daemon older than its command", "error: running daemon cannot honor --launch-assertion; launch refused before custody changes\n", true},
		{"a selection the daemon refused", "error: launch assertion effective agent profiles differ\n", false},
		{"a trusted commit that moved", "error: launch assertion trusted source differs after fresh fetch\n", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			policy := versionSixPolicy(t, pipeline.Reviewer{})
			h, nm := gateHome(t, policy, operatorMachineConfig)
			wt, _ := gatedTask(t, h, "task", policy, claudeGoblin)
			runner := &gateStartRunner{pipelineStartRunner: pipelineStartRunner{worktree: wt}, nativeResult: execx.Result{ExitCode: 1, Stderr: []byte(test.said)}}

			err := pipelineCommand(context.Background(), h, nm, runner, []string{"run", "task", "--intent", "ship safely"}, &bytes.Buffer{})

			if err == nil {
				t.Fatal("a failed launch reported success")
			}
			if isBuild := strings.Contains(err.Error(), "takes no launch selection"); isBuild != test.isBuild {
				t.Fatalf("error = %v, blamed on the build = %v, want %v", err, isBuild, test.isBuild)
			}
		})
	}
}

// A branch's own .no-mistakes.yaml may lower an automatic fix and never raise
// one, so from version 7, whose gate repairs no Test failure, a branch that
// sets auto_fix.test to 1 starts no gate, while version 6 still takes it.
func TestPipelineRunHoldsABranchTestFixToTheFrozenPolicysCount(t *testing.T) {
	versionSix := versionSixPolicy(t, pipeline.Reviewer{})
	versionSeven := pipeline.Policy{Version: 7, AutoFix: pipeline.AutoFix{Rebase: 1}, Classes: versionSix.Classes}
	if err := versionSeven.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		policy  pipeline.Policy
		refusal string
	}{
		{"version 6 takes it", versionSix, ""},
		{"version 7 refuses it", versionSeven, "repository auto_fix.test is 1, outside 0 to the task policy's 0"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			h, nm := gateHome(t, test.policy, operatorMachineConfig)
			wt, _ := gatedTask(t, h, "task", test.policy, claudeGoblin)
			runner := &gateStartRunner{pipelineStartRunner: pipelineStartRunner{worktree: wt, repoConfig: "auto_fix: {test: 1}\n"}}

			// Act
			err := pipelineCommand(context.Background(), h, nm, runner, []string{"run", "task", "--intent", "ship safely"}, &bytes.Buffer{})

			// Assert
			if test.refusal == "" {
				if err != nil || len(runner.native) != 1 {
					t.Fatalf("error = %v, native launches = %+v, want one launch", err, runner.native)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.refusal) {
				t.Fatalf("error = %v, want one naming %q", err, test.refusal)
			}
			if len(runner.native) != 0 {
				t.Fatalf("a refused start launched the gate: %+v", runner.native)
			}
		})
	}
}
