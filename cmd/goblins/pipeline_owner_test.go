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

	"github.com/fpresta0607/code-goblins/internal/crewstate"
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
	ownerIntent = "ship safely"
	// ownerNoTurn is a Test step whose command native gave no turn, as for a
	// missing required environment.
	ownerNoTurn = `{"findings":[{"id":"test-command-no-turn","action":"ask-user","severity":"error","description":"The wrapper reported: goblins gate turn: the command did not start: required test environment: .venv is missing. Respond with fix to run it again."}]}`
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

// atVersionSix freezes task id's policy, and the native config rendered from
// it, at the repository's version 6: its gate still repairs a Test, Lint or CI
// failure, and any caller may resume a CI correction.
func atVersionSix(t *testing.T, h home.Home, nm, id string) {
	t.Helper()
	policy, err := pipeline.Load(filepath.Join("..", "..", "config", "pipeline.json"))
	if err != nil {
		t.Fatal(err)
	}
	policy.Version = 6
	policy.AutoFix = pipeline.AutoFix{Review: 0, Test: 1, Lint: 1, Rebase: 1, CI: 1}
	meta, err := state.ReadTaskMeta(h.State, id)
	if err != nil {
		t.Fatal(err)
	}
	selection, err := policy.Select(meta.PipelineClass)
	if err != nil {
		t.Fatal(err)
	}
	if err := selection.Save(filepath.Join(meta.TaskTmp, "pipeline.json")); err != nil {
		t.Fatal(err)
	}
	meta.PipelineHash = selection.Hash
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	config, _, err := pipeline.Render([]byte("{}"), policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nm, "config.yaml"), config, 0o600); err != nil {
		t.Fatal(err)
	}
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

// ownerSQL runs sql against the fixture gate database under nm.
func ownerSQL(t *testing.T, nm, sql string) {
	t.Helper()
	if out, err := exec.Command("sqlite3", filepath.Join(nm, "state.sqlite"), sql).CombinedOutput(); err != nil {
		t.Fatalf("fixture database: %s %v", out, err)
	}
}

// ownerRunHead gives the gate's bare repository under nm the task branch wt
// holds and, one commit past it, the head its run failed on, which it records
// as the run's head and returns.
func ownerRunHead(t *testing.T, nm, wt string) string {
	t.Helper()
	bare := filepath.Join(nm, "repos", ownerRepo+".git")
	if err := os.MkdirAll(bare, 0o700); err != nil {
		t.Fatal(err)
	}
	gitIn(t, bare, "init", "--quiet", "--bare")
	gitIn(t, wt, "push", "--quiet", bare, "HEAD:refs/heads/"+ownerBranch)
	gate := t.TempDir()
	gitIn(t, gate, "clone", "--quiet", "--branch", ownerBranch, bare, ".")
	head := ownerCommit(t, gate, map[string]string{"a.go": "a3\n"})
	gitIn(t, gate, "push", "--quiet", "origin", "HEAD:refs/heads/"+ownerBranch)
	ownerSQL(t, nm, "UPDATE runs SET head_sha='"+head+"'")
	return head
}

// ownerNative is a native engine that reattaches to a run, stops it, prints
// its terminal preflight for the caller's own head, keeps the admission it is
// given, and resumes a CI handback. Every other command runs for real.
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
	case len(request.Args) == 2 && request.Args[1] == "run", len(request.Args) == 4 && request.Args[1] == "abort", len(request.Args) == 7 && request.Args[1] == "handback":
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
			"predecessor_launch_nonce: ceo-launch",
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
// whose latest gate run, of ownerIntent, is parked at its failed step on that
// head under policy version: the step's log names b.go, and its open finding
// is <step>-failure.
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
	ownerSQL(t, nm, fmt.Sprintf(`CREATE TABLE repos(id TEXT,working_path TEXT,default_branch TEXT);
CREATE TABLE runs(id TEXT,repo_id TEXT,branch TEXT,status TEXT,head_sha TEXT,submitted_head_sha TEXT,intent TEXT,created_at INTEGER);
CREATE TABLE step_results(run_id TEXT,step_name TEXT,step_order INTEGER,status TEXT,findings_json TEXT);
INSERT INTO repos VALUES('%[1]s','%[2]s','main');
INSERT INTO runs VALUES('%[3]s','%[1]s','%[4]s','running','%[5]s','%[5]s','%[9]s',1);
INSERT INTO step_results VALUES('%[3]s','review',1,'completed',NULL);
INSERT INTO step_results VALUES('%[3]s','%[6]s',3,'%[7]s','%[8]s');`, ownerRepo, project, ownerRun, ownerBranch, head, step, stepStatus, findings, ownerIntent))
	return h, nm, wt, selection, &ownerNative{validationGeneration: selection.Hash}
}

// Under version 7 a run that parks at a Test, Lint or CI failure starts no
// fix round: goblins pipeline run returns the step, its findings and its log,
// names the correction the goblin submits, and records that the task is
// fixing them. For a Test or Lint failure it puts the run's head, one commit
// past the head the branch submitted, into the branch and keeps it at an
// archive ref; a CI failure's branch stays where it is. A Test step whose
// command was given no turn stays the CEO's, and version 6 hands nothing back.
func TestPipelineRunHandsAFailedStepBackWithItsFindingsAndLog(t *testing.T) {
	for _, test := range []struct {
		name, step string
		version    int
		findings   string
		correction string
		isMoved    bool
	}{
		{"version 7 test", "test", 7, "", "goblins pipeline validate-authored task --run " + ownerRun + " --findings test-failure", true},
		{"version 7 lint", "lint", 7, "", "goblins pipeline validate-authored task --run " + ownerRun + " --findings lint-failure", true},
		{"version 7 ci", "ci", 7, "", "goblins pipeline handback task --run " + ownerRun, false},
		{"version 7 test command given no turn", "test", 7, ownerNoTurn, "", false},
		{"version 6 test", "test", 6, "", "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			h, nm, wt, _, native := ownerStage(t, test.step, test.version)
			if test.findings != "" {
				ownerSQL(t, nm, "UPDATE step_results SET findings_json='"+test.findings+"' WHERE step_name='"+test.step+"'")
			}
			submitted := gitIn(t, wt, "rev-parse", "HEAD")
			runHead := ownerRunHead(t, nm, wt)
			archive := "refs/heads/archive/handback-" + ownerRun + "-" + runHead
			var out bytes.Buffer

			// Act
			err := pipelineCommand(context.Background(), h, nm, native, []string{"run", "task", "--intent", ownerIntent}, &out)

			// Assert
			if err != nil {
				t.Fatalf("run: %v\n%s", err, out.String())
			}
			if len(native.native) != 1 || native.native[0] != "axi run" {
				t.Fatalf("native calls = %q, want the reattached run alone and no fix round", native.native)
			}
			head := gitIn(t, wt, "rev-parse", "HEAD")
			kept, keptErr := exec.Command("git", "-C", wt, "rev-parse", "--verify", "--quiet", archive).Output()
			if test.isMoved {
				if head != runHead || gitIn(t, wt, "symbolic-ref", "--short", "HEAD") != ownerBranch || keptErr != nil || strings.TrimSpace(string(kept)) != runHead {
					t.Fatalf("branch at %s, %s = %q (%v), want %s at the run's head %s and the archive ref keeping it", head, archive, kept, keptErr, ownerBranch, runHead)
				}
			} else if head != submitted || keptErr == nil {
				t.Fatalf("branch at %s, %s = %q, want the branch left at %s and no archive ref", head, archive, kept, submitted)
			}
			lines, err := state.TailStatus(h.State, "task", 10)
			if err != nil {
				t.Fatal(err)
			}
			if test.correction == "" {
				if strings.Contains(out.String(), "pipeline handback") || strings.Contains(strings.Join(lines, "\n"), "which its gate handed back") {
					t.Fatalf("a failure was handed back:\n%s\n%q", out.String(), lines)
				}
				return
			}
			log := filepath.Join(nm, "logs", ownerRun, test.step+".log")
			for _, want := range []string{
				fmt.Sprintf("pipeline handback: run %s's %s step failed", ownerRun, test.step),
				fmt.Sprintf("pipeline handback: %s-failure (error): the %s command failed; log %s", test.step, test.step, log),
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

// A failed verification escalates the role whose work it checked: under
// version 7 a run's failed Test step moves the task's implement worker one
// tier up its harness, once for that run however often it is read, and
// retires the worker when that tier is its lead's. A worker on the top tier
// stays, a Lint failure moves nothing, nor does a task whose goblin implements
// its own work, and a table that does not load moves nothing while the task
// still reads as working on its findings.
func TestAFailedTestStepEscalatesTheImplementWorkerOneTier(t *testing.T) {
	const table = `{"default_lane":"opus","lanes":{
		"fable":{"harness":"claude","model":"claude-fable-5-2","effort":"xhigh"},
		"opus":{"harness":"claude","model":"claude-opus-5-5","effort":"xhigh"},
		"sonnet":{"harness":"claude","model":"claude-sonnet-5-5","effort":"high"},
		"haiku":{"harness":"claude","model":"haiku","effort":"medium"}},
		"tiers":{"claude":["fable","opus","sonnet","haiku"]}}`
	lanes := map[string][3]string{
		"fable":  {"fable", "claude-fable-5-2", "xhigh"},
		"opus":   {"opus", "claude-opus-5-5", "xhigh"},
		"sonnet": {"sonnet", "claude-sonnet-5-5", "high"},
		"haiku":  {"haiku", "haiku", "medium"},
	}
	for _, test := range []struct {
		name, step, lead, implement, want, routing, said, recorded string
	}{
		{
			name: "a worker one tier below its lead is retired", step: "test", lead: "opus", implement: "sonnet",
			said:     "pipeline handback: a failed test step moves the implement role up one tier, to this task's lead opus claude-opus-5-5 xhigh, so its worker is retired: implement this correction and every later slice yourself",
			recorded: "role-escalated: implement sonnet claude-sonnet-5-5 high -> the lead opus claude-opus-5-5 xhigh after run " + ownerRun + "'s failed test step",
		},
		{
			name: "a worker two tiers below its lead moves up one", step: "test", lead: "fable", implement: "haiku", want: "sonnet",
			said:     "pipeline handback: a failed test step moves the implement role up one tier, to sonnet claude-sonnet-5-5 high, which the task's next relaunch gives its worker; until then implement this correction and every later slice yourself, since this session's worker stays on haiku",
			recorded: "role-escalated: implement haiku haiku medium -> sonnet claude-sonnet-5-5 high after run " + ownerRun + "'s failed test step",
		},
		{name: "a worker on the top tier stays", step: "test", lead: "opus", implement: "fable", want: "fable"},
		{name: "a lint failure moves nothing", step: "lint", lead: "opus", implement: "sonnet", want: "sonnet"},
		{name: "a goblin implementing its own work has no worker to move", step: "test", lead: "opus"},
		{
			name: "a table that does not load moves nothing", step: "test", lead: "opus", implement: "sonnet", want: "sonnet",
			routing: `{"default_lane":`,
			said:    "pipeline handback: no role moved, since the implement role's escalation failed: routing: ",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			h, nm, _, _, native := ownerStage(t, test.step, 7)
			routingTable := table
			if test.routing != "" {
				routingTable = test.routing
			}
			if err := os.WriteFile(filepath.Join(h.Data, "routing.json"), []byte(routingTable), 0o600); err != nil {
				t.Fatal(err)
			}
			meta, err := state.ReadTaskMeta(h.State, "task")
			if err != nil {
				t.Fatal(err)
			}
			lead := lanes[test.lead]
			meta.RouteLane, meta.Model, meta.Effort = lead[0], lead[1], lead[2]
			if test.implement != "" {
				implement := lanes[test.implement]
				meta.ImplementLane, meta.ImplementModel, meta.ImplementEffort = implement[0], implement[1], implement[2]
			}
			if err := state.WriteTaskMeta(h.State, meta); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer

			// Act: the goblin reads the parked run, then reattaches to it.
			for range 2 {
				if err := pipelineCommand(context.Background(), h, nm, native, []string{"run", "task", "--intent", ownerIntent}, &out); err != nil {
					t.Fatalf("run: %v\n%s", err, out.String())
				}
			}

			// Assert
			after, err := state.ReadTaskMeta(h.State, "task")
			if err != nil {
				t.Fatal(err)
			}
			if want := lanes[test.want]; after.ImplementLane != want[0] || after.ImplementModel != want[1] || after.ImplementEffort != want[2] {
				t.Errorf("implement worker = %s %s %s, want %q", after.ImplementLane, after.ImplementModel, after.ImplementEffort, test.want)
			}
			lines, err := state.TailStatus(h.State, "task", 20)
			if err != nil {
				t.Fatal(err)
			}
			status := strings.Join(lines, "\n")
			if verb, _ := crewstate.LatestVerb(lines); verb != "working" {
				t.Errorf("latest verb = %q, want the task still working on its findings", verb)
			}
			if test.recorded == "" {
				if strings.Contains(status, "role-escalated") || strings.Contains(out.String(), "moves the implement role") || after.EscalatedRun != "" {
					t.Fatalf("a role moved, escalated run %q:\n%s\n%s", after.EscalatedRun, out.String(), status)
				}
				if !strings.Contains(out.String(), test.said) {
					t.Errorf("output lacks %q:\n%s", test.said, out.String())
				}
				return
			}
			if strings.Count(status, test.recorded) != 1 || strings.Count(status, "role-escalated") != 1 {
				t.Errorf("status log %q, want the move recorded once: %q", lines, test.recorded)
			}
			if strings.Count(out.String(), test.said) != 1 {
				t.Errorf("output lacks, or repeats, %q:\n%s", test.said, out.String())
			}
			if after.EscalatedRun != ownerRun {
				t.Errorf("escalated run = %q, want %s", after.EscalatedRun, ownerRun)
			}
		})
	}
}

// Under version 7 the goblin that owns a task submits its one-commit
// correction of a Test or Lint failure's findings itself: the run is stopped
// and its correction goes to the run's own validation family, under the
// run's launch and the task's frozen policy, with no CEO admission.
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
			if admission.PredecessorID != ownerRun || admission.LaunchNonce != "ceo-launch" || admission.ValidationGeneration != selection.Hash {
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
// findings of a run still parked at them is refused before the run is
// stopped, naming the CEO's admission.
func TestPipelineOwnerCorrectionRefusesAnythingElseBeforeStoppingTheRun(t *testing.T) {
	for _, test := range []struct {
		name     string
		version  int
		sql      string
		files    map[string]string
		goblin   string
		findings string
		refusal  string
	}{
		{"a change outside the findings' scope", 7, "", map[string]string{"docs/guide.md": "guide2\n"}, "task", "test-failure", "the correction changes docs/guide.md"},
		{"another task's goblin", 7, "", map[string]string{"b.go": "b2\n"}, "other", "test-failure", "own goblin's submission"},
		{"findings that are not the run's", 7, "", map[string]string{"b.go": "b2\n"}, "task", "test-failure,other", "--findings must name exactly run"},
		{"a version 6 task", 6, "", map[string]string{"b.go": "b2\n"}, "task", "test-failure", "hands no failure back"},
		{"a run already stopped", 7, "UPDATE runs SET status='cancelled'; UPDATE step_results SET status='failed' WHERE step_name='test'", map[string]string{"b.go": "b2\n"}, "task", "test-failure", "is parked at no Test or Lint failure its gate handed back"},
		{"a test command given no turn", 7, "UPDATE step_results SET findings_json='" + ownerNoTurn + "' WHERE step_name='test'", map[string]string{"b.go": "b2\n"}, "task", "test-command-no-turn", "is parked at no Test or Lint failure its gate handed back"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			h, nm, wt, _, native := ownerStage(t, "test", test.version)
			if test.sql != "" {
				ownerSQL(t, nm, test.sql)
			}
			ownerCommit(t, wt, test.files)
			inGoblinTerminal(t, h.State, test.goblin)

			// Act
			err := pipelineCommand(context.Background(), h, nm, native, []string{"validate-authored", "task", "--run", ownerRun, "--findings", test.findings}, &bytes.Buffer{})

			// Assert
			if err == nil || !strings.Contains(err.Error(), test.refusal) || !strings.Contains(err.Error(), "goblins pipeline validate-authored task --admission-file <file>") {
				t.Fatalf("error = %v, want one saying %q and naming the CEO's admission", err, test.refusal)
			}
			if len(native.native) != 0 {
				t.Fatalf("a refused correction reached native: %q", native.native)
			}
		})
	}
}

// A CI failure's correction goes back through the native handback, and the
// owning goblin's resume of it is held to the same scope: a correction the
// failure names is resumed, and any other, or a resume while the run is
// parked at a Test failure, is refused before native sees it.
func TestPipelineOwnerCIResumeIsHeldToTheFailuresScope(t *testing.T) {
	for _, test := range []struct {
		name, step string
		files      map[string]string
		goblin     string
		refusal    string
	}{
		{"a change the failure names", "ci", map[string]string{"b.go": "b2\n"}, "task", ""},
		{"a change outside the failure's scope", "ci", map[string]string{"docs/guide.md": "guide2\n"}, "task", "the correction changes docs/guide.md"},
		{"another task's goblin", "ci", map[string]string{"b.go": "b2\n"}, "other", "resumed by task task's own goblin"},
		{"a run parked at a Test failure", "test", map[string]string{"b.go": "b2\n"}, "task", ownerBranch + "'s latest run holds no CI failure its gate handed back"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			h, nm, wt, _, native := ownerStage(t, test.step, 7)
			caller, err := fsx.Canonical(wt)
			if err != nil {
				t.Fatal(err)
			}
			ownerSQL(t, nm, "CREATE TABLE ci_handbacks(id TEXT,run_id TEXT,caller_dir TEXT,released_head TEXT,push_generation INTEGER); INSERT INTO ci_handbacks SELECT 'native-receipt',id,'"+strings.ReplaceAll(caller, "'", "''")+"',head_sha,1 FROM runs;")
			ownerCommit(t, wt, test.files)
			inGoblinTerminal(t, h.State, test.goblin)

			// Act
			err = pipelineCommand(context.Background(), h, nm, native, []string{"handback", "task", "--run", ownerRun, "--resume", "--receipt", "native-receipt"}, &bytes.Buffer{})

			// Assert
			if test.refusal == "" {
				if err != nil || len(native.native) != 1 || !strings.HasPrefix(native.native[0], "axi handback --expect ") || !strings.HasSuffix(native.native[0], " --resume --receipt native-receipt") {
					t.Fatalf("error = %v, native calls = %q, want the goblin's correction resumed through the native handback", err, native.native)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.refusal) || !strings.Contains(err.Error(), "the CEO resumes any other with goblins pipeline handback task --run <run> --resume --receipt <receipt>") {
				t.Fatalf("error = %v, want one saying %q and naming the CEO's resume", err, test.refusal)
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
// fix round: the CEO's or the goblin's --action fix is refused, naming the
// owner's correction. Only a Test rerun of a command given no turn, which asks
// no repair agent, is left to start.
func TestPipelineRespondStartsNoFixRoundForAHandedBackStep(t *testing.T) {
	for _, test := range []struct{ name, step, findings string }{
		{"test", "test", "test-failure"},
		{"lint", "lint", "lint-failure"},
		{"ci", "ci", "ci-failure"},
		{"a test rerun with a repair beside it", "test", "test-command-no-turn,test-failure"},
		{"a rerun of a lint failure", "lint", "test-command-no-turn"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			h, nm, _, _, native := ownerStage(t, test.step, 7)
			inGoblinTerminal(t, h.State, "task")
			runner := &handbackRespondRunner{ownerNative: native, gate: pipeline.Gate{RunID: ownerRun, StepID: "step", Step: test.step, Status: "awaiting_approval", Findings: fmt.Sprintf(`{"findings":[{"id":"%s-failure","action":"auto-fix"},{"id":"test-command-no-turn","action":"ask-user"}]}`, test.step)}}

			// Act
			err := pipelineCommand(context.Background(), h, nm, runner, []string{"respond", "task", "--action", "fix", "--findings", test.findings}, &bytes.Buffer{})

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
