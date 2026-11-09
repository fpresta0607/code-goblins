package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
)

const (
	ownerRun    = "01M70OWNERRUNOFTHISTESTXYZ"
	ownerRepo   = "c0ffee000002"
	ownerBranch = "feat/handback"
)

// ownerPolicy is version 7, whose gate repairs no Test, Lint or CI failure,
// or version 6 before it.
func ownerPolicy(t *testing.T, version int) pipeline.Policy {
	t.Helper()
	policy := versionSixPolicy(t, pipeline.Reviewer{})
	if version == 7 {
		policy.Version = 7
		policy.AutoFix = pipeline.AutoFix{Rebase: 1}
	}
	if err := policy.Validate(); err != nil {
		t.Fatal(err)
	}
	return policy
}

// ownerCommit writes each file into dir and commits them all.
func ownerCommit(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	gitIn(t, dir, "add", "--all")
	gitIn(t, dir, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "commit", "--quiet", "-m", "fixture")
	return gitIn(t, dir, "rev-parse", "HEAD")
}

// ownerNative is a native engine that stops a run, prints its terminal
// preflight for the caller's own head, and keeps the admission it is given.
// Every other command runs for real.
type ownerNative struct {
	validationGeneration string
	native               []string
	admission            []byte
}

func (n *ownerNative) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	if request.Name != "no-mistakes" {
		return execx.OSRunner{}.Run(context.WithoutCancel(ctx), request)
	}
	n.native = append(n.native, strings.Join(request.Args, " "))
	switch {
	case len(request.Args) == 4 && request.Args[1] == "abort":
		return execx.Result{}, nil
	case len(request.Args) == 4 && request.Args[1] == "terminal-preflight":
		head, err := exec.Command("git", "-C", request.Dir, "rev-parse", "HEAD").Output()
		if err != nil {
			return execx.Result{}, err
		}
		tree, err := exec.Command("git", "-C", request.Dir, "rev-parse", "HEAD^{tree}").Output()
		if err != nil {
			return execx.Result{}, err
		}
		return execx.Result{Stdout: []byte(strings.Join([]string{
			"predecessor_id: " + request.Args[3],
			"predecessor_status: cancelled",
			"has_competing_run: false",
			"snapshot_digest: sha256:snapshot",
			"candidate_sha: " + strings.TrimSpace(string(head)),
			"candidate_tree: " + strings.TrimSpace(string(tree)),
			"caller_is_clean: true",
			"predecessor_launch_nonce: cfo-launch",
			"predecessor_validation_generation: " + n.validationGeneration,
			"intent_digest: sha256:intent",
			`predecessor_launch_assertion_json: "{\"trusted_sha\":\"old\",\"apply\":true}"`,
			"published_head_sha: null",
			"push_generation: 0",
			"push_target_fingerprint: null",
			"",
		}, "\n"))}, nil
	case len(request.Args) == 4 && request.Args[1] == "validate-authored":
		data, err := os.ReadFile(request.Args[3])
		n.admission = data
		return execx.Result{Stdout: []byte("authored_validation:\n  automatic_repairs_remaining: 0\n")}, err
	}
	return execx.Result{}, fmt.Errorf("unexpected native command %q", request.Args)
}

// ownerStage is a task whose branch changes a.go from origin's main, and
// whose latest gate run failed its step on that head under policy version:
// the step's log names b.go, and its open finding is <step>-failure.
func ownerStage(t *testing.T, step string, version int) (home.Home, string, string, pipeline.Selection, *ownerNative) {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	root, err := fsx.Canonical(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := home.Home{Root: root, State: filepath.Join(root, "state"), Data: filepath.Join(root, "data")}
	nm := filepath.Join(root, "nm")
	tmp := filepath.Join(h.State, "tasktmp", "task")
	for _, path := range []string{nm, tmp, h.Data, filepath.Join(nm, "logs", ownerRun)} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	origin := filepath.Join(root, "origin.git")
	gitIn(t, root, "init", "--quiet", "--bare", "--initial-branch=main", origin)
	seed := filepath.Join(root, "seed")
	gitIn(t, root, "init", "--quiet", "--initial-branch=main", seed)
	ownerCommit(t, seed, map[string]string{"a.go": "a\n", "b.go": "b\n", "docs/guide.md": "guide\n"})
	gitIn(t, seed, "push", "--quiet", origin, "HEAD:refs/heads/main")
	project := filepath.Join(root, "project")
	gitIn(t, root, "clone", "--quiet", origin, project)
	wt := filepath.Join(project, ".worktrees", "gb-task")
	gitIn(t, project, "worktree", "add", "--quiet", "-b", ownerBranch, wt, "origin/main")
	head := ownerCommit(t, wt, map[string]string{"a.go": "a2\n"})

	policy := ownerPolicy(t, version)
	selection, err := policy.Select("high-risk")
	if err != nil {
		t.Fatal(err)
	}
	config, _, err := pipeline.Render([]byte("{}"), policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nm, "config.yaml"), config, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := selection.Save(filepath.Join(tmp, "pipeline.json")); err != nil {
		t.Fatal(err)
	}
	meta := state.TaskMeta{ID: "task", Mode: "no-mistakes", Worktree: wt, Project: project, TaskTmp: tmp, PipelineClass: selection.Class, PipelineHash: selection.Hash, Harness: "claude", Model: "claude-opus-5-5", Effort: "xhigh"}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nm, "logs", ownerRun, step+".log"), []byte("--- FAIL: TestB (0.00s)\n    b.go:12: want 2, got 3\nFAIL\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stepStatus := "awaiting_approval"
	if step == "ci" {
		stepStatus = "awaiting_agent"
	}
	findings := fmt.Sprintf(`{"findings":[{"id":"%s-failure","severity":"error","description":"the %s command failed"}]}`, step, step)
	schema := fmt.Sprintf(`CREATE TABLE repos(id TEXT,working_path TEXT,default_branch TEXT);
CREATE TABLE runs(id TEXT,repo_id TEXT,branch TEXT,status TEXT,head_sha TEXT,submitted_head_sha TEXT,created_at INTEGER);
CREATE TABLE step_results(run_id TEXT,step_name TEXT,step_order INTEGER,status TEXT,findings_json TEXT);
INSERT INTO repos VALUES('%[1]s','%[2]s','main');
INSERT INTO runs VALUES('%[3]s','%[1]s','%[4]s','running','%[5]s','%[5]s',1);
INSERT INTO step_results VALUES('%[3]s','review',1,'completed',NULL);
INSERT INTO step_results VALUES('%[3]s','%[6]s',3,'%[7]s','%[8]s');`, ownerRepo, project, ownerRun, ownerBranch, head, step, stepStatus, findings)
	if out, err := exec.Command("sqlite3", filepath.Join(nm, "state.sqlite"), schema).CombinedOutput(); err != nil {
		t.Fatalf("fixture database: %s %v", out, err)
	}
	return h, nm, wt, selection, &ownerNative{validationGeneration: selection.Hash}
}

// Under version 7 a run that parks at a Test, Lint or CI failure starts no
// fix round: cfo pipeline run returns the step, its findings and its log,
// names the correction the goblin submits, and records that the task is
// fixing them. Version 6 hands nothing back.
func TestPipelineRunHandsAFailedStepBackWithItsFindingsAndLog(t *testing.T) {
	for _, test := range []struct {
		step, status string
		version      int
		correction   string
	}{
		{"test", "awaiting_approval", 7, "cfo pipeline validate-authored task --run " + ownerRun + " --findings test-failure"},
		{"lint", "awaiting_approval", 7, "cfo pipeline validate-authored task --run " + ownerRun + " --findings lint-failure"},
		{"ci", "awaiting_agent", 7, "cfo pipeline handback task --run " + ownerRun},
		{"test", "awaiting_approval", 6, ""},
	} {
		t.Run(fmt.Sprintf("version %d %s", test.version, test.step), func(t *testing.T) {
			// Arrange
			policy := ownerPolicy(t, test.version)
			h, nm := gateHome(t, policy, operatorMachineConfig)
			wt, _ := gatedTask(t, h, "task", policy, claudeGoblin)
			const head = "0123456789abcdef0123456789abcdef01234567"
			handedBack, err := json.Marshal([]map[string]string{{"run_id": ownerRun, "repo_id": ownerRepo, "branch": "feat/policy", "status": "running", "head": head, "submitted_head": head, "step": test.step, "step_status": test.status, "findings": `{"findings":[{"id":"` + test.step + `-failure","severity":"error","description":"the command failed"}]}`}})
			if err != nil {
				t.Fatal(err)
			}
			runner := &gateStartRunner{pipelineStartRunner: pipelineStartRunner{worktree: wt, handedBack: handedBack}}
			var out bytes.Buffer

			// Act
			err = pipelineCommand(context.Background(), h, nm, runner, []string{"run", "task", "--intent", "ship safely"}, &out)

			// Assert
			if err != nil {
				t.Fatalf("run: %v\n%s", err, out.String())
			}
			if len(runner.native) != 1 {
				t.Fatalf("native calls = %+v, want the run alone and no fix round", runner.native)
			}
			lines, err := state.TailStatus(h.State, "task", 10)
			if err != nil {
				t.Fatal(err)
			}
			if test.correction == "" {
				if strings.Contains(out.String(), "pipeline handback") || strings.Contains(strings.Join(lines, "\n"), "which its gate handed back") {
					t.Fatalf("version 6 handed a failure back:\n%s\n%q", out.String(), lines)
				}
				return
			}
			log := filepath.Join(nm, "logs", ownerRun, test.step+".log")
			for _, want := range []string{
				fmt.Sprintf("pipeline handback: run %s's %s step failed", ownerRun, test.step),
				fmt.Sprintf("pipeline handback: %s-failure (error): the command failed; log %s", test.step, log),
				test.correction,
			} {
				if !strings.Contains(out.String(), want) {
					t.Fatalf("output lacks %q:\n%s", want, out.String())
				}
			}
			if status := strings.Join(lines, "\n"); !strings.Contains(status, fmt.Sprintf("working: fixing run %s's %s findings %s-failure, which its gate handed back", ownerRun, test.step, test.step)) {
				t.Fatalf("status log %q, want the task working on the findings", lines)
			}
		})
	}
}

// Under version 7 the goblin that owns a task submits its one-commit
// correction of a Test or Lint failure's findings itself: the run is stopped
// and its correction goes to the run's own validation family, under the
// run's launch and the task's frozen policy, with no CFO admission.
func TestPipelineOwnerCorrectionIsAdmittedToItsRunsOwnLineage(t *testing.T) {
	for _, step := range []string{"test", "lint"} {
		t.Run(step, func(t *testing.T) {
			// Arrange
			h, nm, wt, selection, native := ownerStage(t, step, 7)
			ownerCommit(t, wt, map[string]string{"b.go": "b2\n"})
			inGoblinTerminal(t, h.State, "task")
			var out bytes.Buffer

			// Act
			err := pipelineCommand(context.Background(), h, nm, native, []string{"validate-authored", "task", "--run", ownerRun, "--findings", step + "-failure"}, &out)

			// Assert
			if err != nil {
				t.Fatalf("validate-authored: %v\n%s", err, out.String())
			}
			if len(native.native) != 3 || native.native[0] != "axi abort --run "+ownerRun || native.native[1] != "axi terminal-preflight --predecessor "+ownerRun || !strings.HasPrefix(native.native[2], "axi validate-authored --admission-file ") {
				t.Fatalf("native calls = %q, want the run stopped, preflighted and its correction admitted", native.native)
			}
			var admission struct {
				PredecessorID        string `json:"predecessor_id"`
				LaunchNonce          string `json:"launch_nonce"`
				ValidationGeneration string `json:"validation_generation"`
				ControllerPolicy     struct {
					SHA256       string `json:"sha256"`
					Class        string `json:"class"`
					ReviewCycles int    `json:"review_cycles"`
				} `json:"controller_policy"`
				Reason string `json:"reason"`
			}
			if err := json.Unmarshal(native.admission, &admission); err != nil {
				t.Fatalf("admission %s: %v", native.admission, err)
			}
			if admission.PredecessorID != ownerRun || admission.LaunchNonce != "cfo-launch" || admission.ValidationGeneration != selection.Hash {
				t.Fatalf("admission = %+v, want run %s's own launch and validation generation", admission, ownerRun)
			}
			if admission.ControllerPolicy.SHA256 != selection.Hash || admission.ControllerPolicy.Class != "high-risk" || admission.ControllerPolicy.ReviewCycles != 3 {
				t.Fatalf("controller policy = %+v, want the task's frozen high-risk cap of 3, renewed by nothing", admission.ControllerPolicy)
			}
			lines, err := state.TailStatus(h.State, "task", 10)
			if err != nil {
				t.Fatal(err)
			}
			status := strings.Join(lines, "\n")
			if !strings.Contains(status, fmt.Sprintf("pipeline-owner-correction: run=%s step=%s findings=%s-failure by=task", ownerRun, step, step)) || !strings.Contains(status, "pipeline-admission: operation=validate-authored") {
				t.Fatalf("status log %q, want the owner's correction and its admission recorded", lines)
			}
		})
	}
}

// Anything but the owning goblin's one commit of exactly the handed-back
// findings is refused before the run is stopped, naming the CFO's admission.
func TestPipelineOwnerCorrectionRefusesAnythingElseBeforeStoppingTheRun(t *testing.T) {
	for _, test := range []struct {
		name     string
		version  int
		files    map[string]string
		goblin   string
		findings string
		refusal  string
	}{
		{"a change outside the findings' scope", 7, map[string]string{"docs/guide.md": "guide2\n"}, "task", "test-failure", "the correction changes docs/guide.md"},
		{"another task's goblin", 7, map[string]string{"b.go": "b2\n"}, "other", "test-failure", "own goblin's submission"},
		{"findings that are not the run's", 7, map[string]string{"b.go": "b2\n"}, "task", "test-failure,other", "--findings must name exactly run"},
		{"a version 6 task", 6, map[string]string{"b.go": "b2\n"}, "task", "test-failure", "hands no failure back"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			h, nm, wt, _, native := ownerStage(t, "test", test.version)
			ownerCommit(t, wt, test.files)
			inGoblinTerminal(t, h.State, test.goblin)

			// Act
			err := pipelineCommand(context.Background(), h, nm, native, []string{"validate-authored", "task", "--run", ownerRun, "--findings", test.findings}, &bytes.Buffer{})

			// Assert
			if err == nil || !strings.Contains(err.Error(), test.refusal) || !strings.Contains(err.Error(), "cfo pipeline validate-authored task --admission-file <file>") {
				t.Fatalf("error = %v, want one saying %q and naming the CFO's admission", err, test.refusal)
			}
			if len(native.native) != 0 {
				t.Fatalf("a refused correction reached native: %q", native.native)
			}
		})
	}
}

// A CI failure's correction goes back through the native handback, and the
// owning goblin's resume of it is held to the same scope.
func TestPipelineOwnerCIResumeIsHeldToTheFailuresScope(t *testing.T) {
	for _, test := range []struct {
		name    string
		files   map[string]string
		goblin  string
		refusal string
	}{
		{"a change outside the failure's scope", map[string]string{"docs/guide.md": "guide2\n"}, "task", "the correction changes docs/guide.md"},
		{"another task's goblin", map[string]string{"b.go": "b2\n"}, "other", "resumed by task task's own goblin"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			h, nm, wt, _, native := ownerStage(t, "ci", 7)
			ownerCommit(t, wt, test.files)
			inGoblinTerminal(t, h.State, test.goblin)

			// Act
			err := pipelineCommand(context.Background(), h, nm, native, []string{"handback", "task", "--run", ownerRun, "--resume", "--receipt", "receipt"}, &bytes.Buffer{})

			// Assert
			if err == nil || !strings.Contains(err.Error(), test.refusal) || !strings.Contains(err.Error(), "the CFO resumes any other with cfo pipeline handback task --run <run> --resume --receipt <receipt>") {
				t.Fatalf("error = %v, want one saying %q and naming the CFO's resume", err, test.refusal)
			}
			if len(native.native) != 0 {
				t.Fatalf("a refused resume reached native: %q", native.native)
			}
		})
	}
}

func TestOwnerCIResumeAdmitsTheGoblinsCorrectionInScope(t *testing.T) {
	// Arrange
	h, nm, wt, selection, native := ownerStage(t, "ci", 7)
	ownerCommit(t, wt, map[string]string{"b.go": "b2\n"})
	inGoblinTerminal(t, h.State, "task")
	meta, err := state.ReadTaskMeta(h.State, "task")
	if err != nil {
		t.Fatal(err)
	}

	// Act
	err = checkOwnerCIResume(context.Background(), h, pipeline.Reader{Root: nm, Commands: native}, meta, selection, wt, ownerBranch)

	// Assert
	if err != nil {
		t.Fatalf("an in-scope CI correction was refused: %v", err)
	}
}

// Under version 7 a gate that parks at a Test, Lint or CI failure starts no
// fix round: the CFO's or the goblin's --action fix is refused, naming the
// owner's correction.
func TestPipelineRespondStartsNoFixRoundForAHandedBackStep(t *testing.T) {
	for _, step := range []string{"test", "lint", "ci"} {
		t.Run(step, func(t *testing.T) {
			// Arrange
			h, nm, _, _, native := ownerStage(t, step, 7)
			inGoblinTerminal(t, h.State, "task")
			runner := &handbackRespondRunner{ownerNative: native, gate: pipeline.Gate{RunID: ownerRun, StepID: "step", Step: step, Status: "awaiting_approval", Findings: fmt.Sprintf(`{"findings":[{"id":"%s-failure","action":"auto-fix"}]}`, step)}}

			// Act
			err := pipelineCommand(context.Background(), h, nm, runner, []string{"respond", "task", "--action", "fix", "--findings", step + "-failure"}, &bytes.Buffer{})

			// Assert
			if err == nil || !strings.Contains(err.Error(), "no fix round starts") {
				t.Fatalf("error = %v, want the fix refused", err)
			}
			if len(native.native) != 0 {
				t.Fatalf("a refused fix reached native: %q", native.native)
			}
		})
	}
}

// handbackRespondRunner answers the gate respond reads as parked at gate,
// and runs everything else as ownerNative does.
type handbackRespondRunner struct {
	*ownerNative
	gate pipeline.Gate
}

func (r *handbackRespondRunner) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	if request.Name == "sqlite3" && strings.Contains(request.Args[len(request.Args)-1], "AS step_id") {
		data, err := json.Marshal([]pipeline.Gate{r.gate})
		return execx.Result{Stdout: data}, err
	}
	return r.ownerNative.Run(ctx, request)
}
