package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
)

var (
	claudeGoblin = pipeline.Reviewer{Harness: "claude", Model: "claude-opus-5-5", Effort: "xhigh"}
	codexGoblin  = pipeline.Reviewer{Harness: "codex", Model: "gpt-6.1-sol", Effort: "high"}
)

// gateStartRunner answers a gate start as pipelineStartRunner does, and a
// harness's own sign-in status command as that harness answers it.
type gateStartRunner struct {
	pipelineStartRunner
	signedIn map[string]bool
	probes   []string
	// started and release, when set, hold the native launch open.
	started, release chan struct{}
	nativeResult     execx.Result
}

func (r *gateStartRunner) Run(ctx context.Context, q execx.Request) (execx.Result, error) {
	switch q.Name {
	case "claude":
		r.probes = append(r.probes, q.Name)
		if r.signedIn[q.Name] {
			return execx.Result{Stdout: []byte(`{"loggedIn":true}`)}, nil
		}
		return execx.Result{Stdout: []byte(`{"loggedIn":false}`), ExitCode: 1}, nil
	case "codex":
		r.probes = append(r.probes, q.Name)
		if r.signedIn[q.Name] {
			return execx.Result{Stdout: []byte("Logged in using ChatGPT\n")}, nil
		}
		return execx.Result{Stderr: []byte("Not logged in\n"), ExitCode: 1}, nil
	case "no-mistakes":
		if r.started != nil {
			close(r.started)
			<-r.release
		}
		r.native = append(r.native, q)
		return r.nativeResult, nil
	}
	return r.pipelineStartRunner.Run(ctx, q)
}

// versionSixPolicy is the policy whose gate runs on its task's own harness,
// with the fallback the operator named, if any.
func versionSixPolicy(t *testing.T, fallback pipeline.Reviewer) pipeline.Policy {
	t.Helper()
	policy, err := pipeline.Load(filepath.Join("..", "..", "config", "pipeline.json"))
	if err != nil {
		t.Fatal(err)
	}
	policy.Version, policy.Primary, policy.Reviewer, policy.Fixer, policy.Fallback = 6, pipeline.Reviewer{}, pipeline.Reviewer{}, pipeline.Reviewer{}, fallback
	if err := policy.Validate(); err != nil {
		t.Fatal(err)
	}
	return policy
}

// gateHome is a scratch home and gate root whose machine config the policy
// rendered once, over the operator's own chain and Codex default.
func gateHome(t *testing.T, policy pipeline.Policy, machine string) (home.Home, string) {
	t.Helper()
	root := t.TempDir()
	h := home.Home{Root: root, State: filepath.Join(root, "state")}
	nm := filepath.Join(root, "nm")
	if err := os.MkdirAll(nm, 0o700); err != nil {
		t.Fatal(err)
	}
	config, _, err := pipeline.Render([]byte(machine), policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nm, "config.yaml"), config, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nm, "state.sqlite"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return h, nm
}

const operatorMachineConfig = "agent: [claude]\nagent_config:\n  codex: {model: gpt-6.1-sol, effort: xhigh}\n"

// gatedTask records a gated task on the goblin's harness under the policy and
// returns its worktree and frozen selection.
func gatedTask(t *testing.T, h home.Home, id string, policy pipeline.Policy, goblin pipeline.Reviewer) (string, pipeline.Selection) {
	t.Helper()
	tmp := filepath.Join(h.State, "tasktmp", id)
	project := filepath.Join(h.Root, "project")
	wt := filepath.Join(project, ".worktrees", "gb-"+id)
	for _, path := range []string{tmp, wt} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	selection, err := policy.Select("ordinary")
	if err != nil {
		t.Fatal(err)
	}
	if err := selection.Save(filepath.Join(tmp, "pipeline.json")); err != nil {
		t.Fatal(err)
	}
	meta := state.TaskMeta{ID: id, Mode: "no-mistakes", Worktree: wt, Project: project, TaskTmp: tmp, PipelineClass: selection.Class, PipelineHash: selection.Hash, Harness: goblin.Harness, Model: goblin.Model, Effort: goblin.Effort}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	return wt, selection
}

type writtenSelection struct {
	TrustedSHA string                         `json:"trusted_sha"`
	Apply      bool                           `json:"apply"`
	Profiles   map[string][]map[string]string `json:"profiles"`
}

// launchedSelection reads the launch selection the one native launch named,
// with the nonce and validation generation it was bound to.
func launchedSelection(t *testing.T, native []execx.Request) (selection writtenSelection, raw, nonce, generation string) {
	t.Helper()
	if len(native) != 1 {
		t.Fatalf("native launches=%+v, want one", native)
	}
	args := native[0].Args
	if len(args) != 10 || strings.Join(args[:4], " ") != "axi run --intent ship safely" || args[4] != "--launch-nonce" || args[6] != "--validation-generation" || args[8] != "--launch-assertion" {
		t.Fatalf("native launch argv: %q", args)
	}
	data, err := os.ReadFile(args[9])
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &selection); err != nil {
		t.Fatalf("launch selection %s: %v", data, err)
	}
	return selection, string(data), args[5], args[7]
}

func harnesses(chain []map[string]string) []string {
	var names []string
	for _, entry := range chain {
		names = append(names, entry["harness"])
	}
	return names
}

func TestPipelineRunGivesEachTaskAGateOnItsOwnHarness(t *testing.T) {
	for _, test := range []struct {
		goblin pipeline.Reviewer
		other  string
	}{{claudeGoblin, "codex"}, {codexGoblin, "claude"}} {
		t.Run(test.goblin.Harness, func(t *testing.T) {
			policy := versionSixPolicy(t, pipeline.Reviewer{})
			h, nm := gateHome(t, policy, operatorMachineConfig)
			wt, frozen := gatedTask(t, h, "task", policy, test.goblin)
			runner := &gateStartRunner{pipelineStartRunner: pipelineStartRunner{worktree: wt}}
			var out bytes.Buffer

			if err := pipelineCommand(context.Background(), h, nm, runner, []string{"run", "task", "--intent", "ship safely"}, &out); err != nil {
				t.Fatalf("pipelineCommand: %v", err)
			}

			selection, raw, nonce, generation := launchedSelection(t, runner.native)
			if !selection.Apply || selection.TrustedSHA != "0123456789abcdef0123456789abcdef01234567" {
				t.Fatalf("launch selection = %s, want it applied at the trusted commit", raw)
			}
			for _, role := range []string{"primary", "reviewer", "fixer"} {
				chain := selection.Profiles[role]
				if len(chain) != 1 || chain[0]["harness"] != test.goblin.Harness || chain[0]["model"] != test.goblin.Model || chain[0]["effort"] != test.goblin.Effort {
					t.Fatalf("%s runs %v, want the goblin's own %+v alone", role, chain, test.goblin)
				}
			}
			if len(selection.Profiles) != 3 || strings.Contains(raw, test.other) {
				t.Fatalf("a %s goblin's gate names another harness or role: %s", test.goblin.Harness, raw)
			}
			if nonce == "" || generation != frozen.Hash {
				t.Fatalf("launch bound to nonce %q and generation %q, want a nonce and the frozen policy %s", nonce, generation, frozen.Hash)
			}
			if len(runner.probes) != 0 {
				t.Fatalf("a gate with no fallback named asked about %v", runner.probes)
			}
			if want := "pipeline gate agent: " + test.goblin.Harness + " " + test.goblin.Model + " " + test.goblin.Effort + ", the task's own harness, with no fallback named\n"; !strings.Contains(out.String(), want) {
				t.Fatalf("output %q does not say %q", out.String(), want)
			}
		})
	}
}

func TestTwoTasksOnDifferentHarnessesGateAtOnceUnderOneMachineConfig(t *testing.T) {
	policy := versionSixPolicy(t, pipeline.Reviewer{})
	h, nm := gateHome(t, policy, operatorMachineConfig)
	machine, err := os.ReadFile(filepath.Join(nm, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	claudeWorktree, _ := gatedTask(t, h, "claude-task", policy, claudeGoblin)
	codexWorktree, _ := gatedTask(t, h, "codex-task", policy, codexGoblin)
	// The Claude task's gate is held open inside no-mistakes while the Codex
	// task's starts and returns.
	held := &gateStartRunner{pipelineStartRunner: pipelineStartRunner{worktree: claudeWorktree}, started: make(chan struct{}), release: make(chan struct{})}
	first := make(chan error, 1)
	go func() {
		first <- pipelineCommand(context.Background(), h, nm, held, []string{"run", "claude-task", "--intent", "ship safely"}, &bytes.Buffer{})
	}()
	<-held.started

	second := &gateStartRunner{pipelineStartRunner: pipelineStartRunner{worktree: codexWorktree}}
	if err := pipelineCommand(context.Background(), h, nm, second, []string{"run", "codex-task", "--intent", "ship safely"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("a Codex task could not start its gate while a Claude task's ran: %v", err)
	}
	close(held.release)
	if err := <-first; err != nil {
		t.Fatalf("Claude task: %v", err)
	}

	claude, _, claudeNonce, _ := launchedSelection(t, held.native)
	codex, _, codexNonce, _ := launchedSelection(t, second.native)
	for _, role := range []string{"primary", "reviewer", "fixer"} {
		if got := harnesses(claude.Profiles[role]); !reflect.DeepEqual(got, []string{"claude"}) {
			t.Fatalf("the Claude task's %s runs %v", role, got)
		}
		if got := harnesses(codex.Profiles[role]); !reflect.DeepEqual(got, []string{"codex"}) {
			t.Fatalf("the Codex task's %s runs %v", role, got)
		}
	}
	if claudeNonce == codexNonce {
		t.Fatalf("both launches share the nonce %q", claudeNonce)
	}
	if held.native[0].Args[9] == second.native[0].Args[9] {
		t.Fatalf("both tasks wrote one launch selection file: %s", second.native[0].Args[9])
	}
	after, err := os.ReadFile(filepath.Join(nm, "config.yaml"))
	if err != nil || !bytes.Equal(after, machine) {
		t.Fatalf("a gate start changed the machine config: %v\n%s", err, after)
	}
}

func TestPipelineRunStartsNoSecondHarnessUnlessNamedAndSignedIn(t *testing.T) {
	for _, test := range []struct {
		name       string
		fallback   pipeline.Reviewer
		isSignedIn bool
		want       []string
		wantProbes []string
		wantOutput string
	}{
		{"none named", pipeline.Reviewer{}, true, []string{"claude"}, nil, "with no fallback named\n"},
		{"named and signed in", codexGoblin, true, []string{"claude", "codex"}, []string{"codex"}, "then the named fallback codex gpt-6.1-sol high"},
		{"named and signed out", codexGoblin, false, []string{"claude"}, []string{"codex"}, "The named fallback codex is not signed in, so it is left out"},
		{"named on the task's own harness", pipeline.Reviewer{Harness: "claude", Model: "sonnet", Effort: "medium"}, true, []string{"claude"}, nil, "with no fallback named on another harness\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			policy := versionSixPolicy(t, test.fallback)
			h, nm := gateHome(t, policy, operatorMachineConfig)
			wt, _ := gatedTask(t, h, "task", policy, claudeGoblin)
			runner := &gateStartRunner{pipelineStartRunner: pipelineStartRunner{worktree: wt}, signedIn: map[string]bool{"codex": test.isSignedIn, "claude": test.isSignedIn}}
			var out bytes.Buffer

			if err := pipelineCommand(context.Background(), h, nm, runner, []string{"run", "task", "--intent", "ship safely"}, &out); err != nil {
				t.Fatalf("pipelineCommand: %v", err)
			}

			selection, raw, _, _ := launchedSelection(t, runner.native)
			for _, role := range []string{"primary", "reviewer", "fixer"} {
				if got := harnesses(selection.Profiles[role]); !reflect.DeepEqual(got, test.want) {
					t.Fatalf("%s runs %v, want %v: %s", role, got, test.want, raw)
				}
			}
			if !reflect.DeepEqual(runner.probes, test.wantProbes) {
				t.Fatalf("sign-in asked of %v, want %v", runner.probes, test.wantProbes)
			}
			if !strings.Contains(out.String(), test.wantOutput) {
				t.Fatalf("output %q does not say %q", out.String(), test.wantOutput)
			}
		})
	}
}

func TestPipelineRunTakesAnUnnamedModelOrEffortFromTheMachineConfig(t *testing.T) {
	policy := versionSixPolicy(t, pipeline.Reviewer{})
	unnamed := pipeline.Reviewer{Harness: "codex", Effort: "default"}
	t.Run("the operator's default", func(t *testing.T) {
		h, nm := gateHome(t, policy, operatorMachineConfig)
		wt, _ := gatedTask(t, h, "task", policy, unnamed)
		runner := &gateStartRunner{pipelineStartRunner: pipelineStartRunner{worktree: wt}}

		if err := pipelineCommand(context.Background(), h, nm, runner, []string{"run", "task", "--intent", "ship safely"}, &bytes.Buffer{}); err != nil {
			t.Fatalf("pipelineCommand: %v", err)
		}

		selection, raw, _, _ := launchedSelection(t, runner.native)
		if chain := selection.Profiles["primary"]; len(chain) != 1 || chain[0]["harness"] != "codex" || chain[0]["model"] != "gpt-6.1-sol" || chain[0]["effort"] != "xhigh" {
			t.Fatalf("launch selection = %s, want Codex on the machine's own default", raw)
		}
	})
	t.Run("no default", func(t *testing.T) {
		h, nm := gateHome(t, policy, "agent: [claude]\n")
		wt, _ := gatedTask(t, h, "task", policy, unnamed)
		runner := &gateStartRunner{pipelineStartRunner: pipelineStartRunner{worktree: wt}}

		err := pipelineCommand(context.Background(), h, nm, runner, []string{"run", "task", "--intent", "ship safely"}, &bytes.Buffer{})

		if err == nil || !strings.Contains(err.Error(), "cfo switch task --model") || !strings.Contains(err.Error(), "agent_config.codex") {
			t.Fatalf("error = %v, want both ways to name the model and effort", err)
		}
		if len(runner.native) != 0 {
			t.Fatalf("a gate with no model started: %+v", runner.native)
		}
	})
}

func TestPipelineRunRefusesAGateForAHarnessItCannotProve(t *testing.T) {
	policy := versionSixPolicy(t, codexGoblin)
	h, nm := gateHome(t, policy, operatorMachineConfig)
	wt, _ := gatedTask(t, h, "task", policy, pipeline.Reviewer{Harness: "pi", Model: "anthropic/claude-opus-5-5", Effort: "high"})
	runner := &gateStartRunner{pipelineStartRunner: pipelineStartRunner{worktree: wt}, signedIn: map[string]bool{"codex": true}}

	err := pipelineCommand(context.Background(), h, nm, runner, []string{"run", "task", "--intent", "ship safely"}, &bytes.Buffer{})

	if err == nil || !strings.Contains(err.Error(), "pi") {
		t.Fatalf("error = %v, want the pi task refused by name", err)
	}
	if len(runner.native) != 0 || len(runner.probes) != 0 {
		t.Fatalf("a pi task's gate started %+v after asking %v, though only its own harness may gate it first", runner.native, runner.probes)
	}
}

func TestPipelineRunNamesTheBuildALaunchSelectionNeeds(t *testing.T) {
	policy := versionSixPolicy(t, pipeline.Reviewer{})
	h, nm := gateHome(t, policy, operatorMachineConfig)
	wt, _ := gatedTask(t, h, "task", policy, claudeGoblin)
	runner := &gateStartRunner{pipelineStartRunner: pipelineStartRunner{worktree: wt}, nativeResult: execx.Result{ExitCode: 1, Stderr: []byte("Error: unknown flag: --launch-assertion\n")}}

	err := pipelineCommand(context.Background(), h, nm, runner, []string{"run", "task", "--intent", "ship safely"}, &bytes.Buffer{})

	if err == nil || !strings.Contains(err.Error(), "launch selection") || !strings.Contains(err.Error(), "docs/pipeline.md") {
		t.Fatalf("error = %v, want the missing launch selection support named", err)
	}
}
