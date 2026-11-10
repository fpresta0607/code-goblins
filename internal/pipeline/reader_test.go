package pipeline

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"gopkg.in/yaml.v3"
)

func TestGateReadsLatestBranchRoundFromSQLite(t *testing.T) {
	sqlite, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	dir := t.TempDir()
	sql := `CREATE TABLE repos(id TEXT,working_path TEXT,default_branch TEXT);
CREATE TABLE runs(id TEXT,repo_id TEXT,branch TEXT,created_at INTEGER,status TEXT);
CREATE TABLE step_results(id TEXT,run_id TEXT,step_name TEXT,status TEXT,auto_fix_limit INTEGER,findings_json TEXT,step_order INTEGER);
CREATE TABLE step_rounds(step_result_id TEXT,round INTEGER,selection_source TEXT);
INSERT INTO repos VALUES('repo','C:\project','main');
INSERT INTO runs VALUES('old','repo','feat',1,'completed'),('current','repo','feat',2,'running'),('other','repo','other',3,'running');
INSERT INTO step_results VALUES('oldstep','old','review','completed',10,'{}',1),('step','current','review','awaiting_approval',0,'{"findings":[]}',1),('otherstep','other','review','awaiting_approval',0,'{}',1);
INSERT INTO step_rounds VALUES('step',1,'user'),('step',2,'user'),('step',3,NULL);`
	if out, err := exec.Command(sqlite, filepath.Join(dir, "state.sqlite"), sql).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %s %v", out, err)
	}
	reader := Reader{Root: dir, Commands: fixtureCommands{execx.OSRunner{}}}
	gate, err := reader.Gate(context.Background(), "C:/project", "feat")
	if err != nil || gate.RunID != "current" || gate.Round != 3 || gate.Selected != "" || gate.AutoFixLimit == nil || *gate.AutoFixLimit != 0 {
		t.Fatalf("gate: %+v %v", gate, err)
	}
	if _, err := reader.Gate(context.Background(), "C:/project", "absent"); err == nil {
		t.Fatal("absent branch inherited another gate")
	}
	if out, err := exec.Command(sqlite, filepath.Join(dir, "state.sqlite"), `UPDATE step_rounds SET selection_source='user' WHERE step_result_id='step' AND round=3`).CombinedOutput(); err != nil {
		t.Fatalf("selection: %s %v", out, err)
	}
	gate, err = reader.Gate(context.Background(), "C:/project", "feat")
	if err != nil || gate.Selected != "user" {
		t.Fatalf("selection lost: %+v %v", gate, err)
	}
}

// A run named for a gate decision is read as its branch only while it is that
// branch's latest run in this project, since the native command that answers
// it answers the latest run of the branch checked out where it runs.
func TestRunBranchNamesOnlyALatestRunOfThisProject(t *testing.T) {
	sqlite, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	// Arrange
	dir := t.TempDir()
	sql := `CREATE TABLE repos(id TEXT,working_path TEXT,default_branch TEXT);
CREATE TABLE runs(id TEXT,repo_id TEXT,branch TEXT,created_at INTEGER,status TEXT);
INSERT INTO repos VALUES('repo','C:\project','main'),('elsewhere','C:\other','main');
INSERT INTO runs VALUES('old','repo','fix/connections',1,'failed'),('current','repo','fix/connections',2,'running'),('panel','repo','feat/panel-row',3,'completed'),('foreign','elsewhere','fix/connections',4,'running');`
	if out, err := exec.Command(sqlite, filepath.Join(dir, "state.sqlite"), sql).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %s %v", out, err)
	}
	reader := Reader{Root: dir, Commands: fixtureCommands{execx.OSRunner{}}}
	for _, tc := range []struct {
		run, want string
	}{
		{"current", "fix/connections"},
		{"panel", "feat/panel-row"},
		{"old", ""},
		{"foreign", ""},
		{"absent", ""},
		{"' OR '1'='1", ""},
	} {
		// Act
		branch, err := reader.RunBranch(context.Background(), "C:/project", tc.run)

		// Assert
		if tc.want == "" && (err == nil || branch != "") {
			t.Errorf("run %q read as branch %q, %v; want a refusal", tc.run, branch, err)
		}
		if tc.want != "" && (err != nil || branch != tc.want) {
			t.Errorf("run %q read as branch %q, %v; want %q", tc.run, branch, err, tc.want)
		}
	}
}

func TestRepoPolicyCannotRaiseCapsAndDoesNotOwnReviewAgents(t *testing.T) {
	p := versionFive()
	for _, source := range []string{"auto_fix: {review: 10}", "auto_fix: {test: 2}", "auto_fix: {review: 0, review: 10}", "agent: [claude]\n---\nagent: [pi]"} {
		if err := CheckRepoConfig([]byte(source), p); err == nil {
			t.Errorf("unsafe override accepted: %s", source)
		}
	}
	for _, source := range []string{"agent: [claude]\nauto_fix: {review: 0, test: 1}", "agent: codex", "agent: [claude, codex]", "disable_project_settings: true"} {
		if err := CheckRepoConfig([]byte(source), p); err != nil {
			t.Errorf("valid override refused: %s: %v", source, err)
		}
	}
	if err := CheckRepoConfig([]byte("auto_fix: {babysit: 10}"), p); err == nil {
		t.Error("legacy CI alias bypassed automatic budget")
	}
}

// The adversary is a branch editing its own .no-mistakes.yaml, which the
// automatic-fix check reads from the submitted branch. Lowering a count only
// sends more to a person, so it is accepted; raising one, a negative count,
// or a key the policy does not govern could let a gate fix its way past a
// decision, so each is refused.
func TestRepoPolicyMayLowerAnAutomaticFixButNeverRaiseOne(t *testing.T) {
	// Arrange
	p := versionFive()

	for _, source := range []string{"auto_fix: {ci: 0}", "auto_fix: {test: 0, lint: 0, rebase: 0, ci: 0}", "auto_fix: {babysit: 0}", "auto_fix: {review: 0, test: 1, lint: 1, rebase: 1, ci: 1}"} {
		// Act
		err := CheckRepoConfig([]byte(source), p)

		// Assert
		if err != nil {
			t.Errorf("a lowered or equal automatic fix was refused: %s: %v", source, err)
		}
	}
	for _, source := range []string{"auto_fix: {ci: 2}", "auto_fix: {rebase: 3}", "auto_fix: {babysit: 2}", "auto_fix: {ci: -1}", "auto_fix: {document: 1}", "auto_fix: {ci: 0, fixit: 0}"} {
		// Act
		err := CheckRepoConfig([]byte(source), p)

		// Assert
		if err == nil {
			t.Errorf("a raised, negative or ungoverned automatic fix was accepted: %s", source)
		}
	}
	checks := testPolicy(t)
	if err := CheckRepoConfig([]byte("auto_fix: {review: 0, test: 0, lint: 0, rebase: 1, ci: 0}"), checks); err != nil {
		t.Errorf("version %d refused its own counts: %v", checks.Version, err)
	}
	for _, source := range []string{"auto_fix: {test: 1}", "auto_fix: {lint: 1}", "auto_fix: {ci: 1}"} {
		if err := CheckRepoConfig([]byte(source), checks); err == nil {
			t.Errorf("version %d accepted a follow-up its gate no longer takes: %s", checks.Version, source)
		}
	}
}

func TestRepoAgentCannotAlterRenderedGlobalReviewAgents(t *testing.T) {
	chain := versionFive()
	pinned := chain
	pinned.Version, pinned.Fallback = 3, Reviewer{}
	pinned.Reviewer, pinned.Fixer = pinned.Primary, pinned.Primary
	for _, source := range []string{"agent: claude", "agent: codex", "agent: [claude, codex]"} {
		if err := CheckRepoConfig([]byte(source), chain); err != nil {
			t.Fatalf("repository agent refused: %s: %v", source, err)
		}
		global := []byte(source + "\nreview_agents:\n  reviewer: {agent: claude}\n  fixer: {agent: claude}\n")
		var config struct {
			Agent        []string `yaml:"agent"`
			ReviewAgents map[string]struct {
				Agent  string `yaml:"agent"`
				Model  string `yaml:"model"`
				Effort string `yaml:"effort"`
			} `yaml:"review_agents"`
		}
		// Version 3 pins both review roles to its Codex profile.
		rendered, _, err := Render(global, pinned)
		if err != nil {
			t.Fatal(err)
		}
		if err := yaml.Unmarshal(rendered, &config); err != nil {
			t.Fatal(err)
		}
		for _, role := range []string{"reviewer", "fixer"} {
			profile := config.ReviewAgents[role]
			if profile.Agent != "codex" || profile.Model != "gpt-6.1-sol" || profile.Effort != "xhigh" {
				t.Fatalf("repository %q changed global %s profile: %+v", source, role, profile)
			}
		}
		// From version 4 every role runs the chain, so no role is left pinned
		// to a harness of the operator's choosing.
		rendered, _, err = Render(global, chain)
		if err != nil {
			t.Fatal(err)
		}
		config.Agent, config.ReviewAgents = nil, nil
		if err := yaml.Unmarshal(rendered, &config); err != nil {
			t.Fatal(err)
		}
		if strings.Join(config.Agent, ",") != "codex,claude" || config.ReviewAgents != nil {
			t.Fatalf("repository %q left global roles off the chain: agent=%v review_agents=%+v", source, config.Agent, config.ReviewAgents)
		}
	}
}

type primaryRoutingRunner struct {
	task    []byte
	trusted []byte
	head    string
}

const primaryRoutingSHA = "0123456789abcdef0123456789abcdef01234567"

func (r *primaryRoutingRunner) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	if request.Name == "sqlite3" {
		return fixtureCommands{execx.OSRunner{}}.Run(ctx, request)
	}
	if request.Name != "git" {
		return execx.Result{}, errors.New("unexpected command")
	}
	switch strings.Join(request.Args, " ") {
	case "rev-parse HEAD":
		head := r.head
		if head == "" {
			head = primaryRoutingSHA
		}
		return execx.Result{Stdout: []byte(head + "\n")}, nil
	case "status --porcelain --untracked-files=all":
		return execx.Result{}, nil
	case "show HEAD:.no-mistakes.yaml":
		if len(r.task) != 0 {
			return execx.Result{Stdout: r.task}, nil
		}
		return execx.Result{Stdout: []byte("agent: claude\nauto_fix: {review: 0, test: 1, lint: 1, rebase: 1, ci: 1}\n")}, nil
	case "ls-remote --symref origin HEAD":
		return execx.Result{Stdout: []byte("ref: refs/heads/main\tHEAD\n" + primaryRoutingSHA + "\tHEAD\n")}, nil
	case "rev-parse --verify refs/remotes/origin/main":
		return execx.Result{Stdout: []byte(primaryRoutingSHA + "\n")}, nil
	case "show refs/remotes/origin/main:.no-mistakes.yaml":
		if len(r.trusted) != 0 {
			return execx.Result{Stdout: r.trusted}, nil
		}
		return execx.Result{Stdout: []byte("auto_fix: {review: 0}\n")}, nil
	default:
		return execx.Result{}, errors.New("unexpected git command")
	}
}

func runPipelineGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %s: %v", strings.Join(args, " "), output, err)
	}
	return strings.TrimSpace(string(output))
}

func TestCheckStartRefusesStaleTrustedPrimaryWithoutUpdatingTrackingRef(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git CLI not available")
	}
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	seed := filepath.Join(root, "seed")
	project := filepath.Join(root, "project")
	state := filepath.Join(root, "state")
	for _, dir := range []string{remote, seed, state} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	runPipelineGit(t, remote, "init", "--bare", "--initial-branch=main")
	runPipelineGit(t, seed, "init", "--initial-branch=main")
	runPipelineGit(t, seed, "config", "user.name", "Pipeline Test")
	runPipelineGit(t, seed, "config", "user.email", "pipeline@example.com")
	safeConfig := []byte("auto_fix: {review: 0, test: 1, lint: 1, rebase: 1, ci: 1}\n")
	if err := os.WriteFile(filepath.Join(seed, ".no-mistakes.yaml"), safeConfig, 0o600); err != nil {
		t.Fatal(err)
	}
	runPipelineGit(t, seed, "add", ".no-mistakes.yaml")
	runPipelineGit(t, seed, "commit", "-m", "safe primary")
	runPipelineGit(t, seed, "remote", "add", "origin", remote)
	runPipelineGit(t, seed, "push", "-u", "origin", "main")
	runPipelineGit(t, root, "clone", remote, project)
	before := runPipelineGit(t, project, "rev-parse", "refs/remotes/origin/main")
	if err := os.WriteFile(filepath.Join(seed, ".no-mistakes.yaml"), []byte("agent: claude\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runPipelineGit(t, seed, "add", ".no-mistakes.yaml")
	runPipelineGit(t, seed, "commit", "-m", "unsafe primary")
	runPipelineGit(t, seed, "push", "origin", "main")
	remoteHead := runPipelineGit(t, seed, "rev-parse", "HEAD")
	if before == remoteHead {
		t.Fatal("remote fixture did not advance")
	}
	sql := `CREATE TABLE repos(id TEXT,working_path TEXT,default_branch TEXT);
CREATE TABLE runs(id TEXT,repo_id TEXT,branch TEXT,created_at INTEGER,status TEXT,head_sha TEXT,submitted_head_sha TEXT,intent TEXT);
INSERT INTO repos VALUES('repo',` + sqlString(filepath.ToSlash(project)) + `,'main');`
	if out, err := exec.Command("sqlite3", filepath.Join(state, "state.sqlite"), sql).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %s %v", out, err)
	}
	reader := Reader{Root: state, Commands: fixtureCommands{execx.OSRunner{}}}
	_, err := reader.CheckStart(context.Background(), project, project, "feature", "ship safely", versionFive())
	if err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("CheckStart error=%v, want stale trusted primary refusal", err)
	}
	after := runPipelineGit(t, project, "rev-parse", "refs/remotes/origin/main")
	if after != before {
		t.Fatalf("origin tracking ref changed from %s to %s", before, after)
	}
}

func TestCheckStartUsesOnlyCodexForTheTrustedPrimaryRole(t *testing.T) {
	sqlite, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	project := filepath.Join(t.TempDir(), "project")
	dir := t.TempDir()
	sql := `CREATE TABLE repos(id TEXT,working_path TEXT,default_branch TEXT);
CREATE TABLE runs(id TEXT,repo_id TEXT,branch TEXT,created_at INTEGER,status TEXT,head_sha TEXT,submitted_head_sha TEXT,intent TEXT);
INSERT INTO repos VALUES('repo',` + sqlString(filepath.ToSlash(project)) + `,'main');`
	if out, err := exec.Command(sqlite, filepath.Join(dir, "state.sqlite"), sql).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %s %v", out, err)
	}

	for _, test := range []struct {
		name    string
		task    string
		trusted string
		wantErr bool
	}{
		{name: "inherits global", trusted: "auto_fix: {review: 0}\n"},
		{name: "explicit codex", trusted: "agent: codex\n"},
		{name: "explicit codex list", trusted: "agent: [codex]\n"},
		{name: "claude override", trusted: "agent: claude\n", wantErr: true},
		{name: "fallback list", trusted: "agent: [codex, claude]\n", wantErr: true},
		{name: "automatic selection", trusted: "agent: auto\n", wantErr: true},
		{name: "trusted command opt-in inherits global", task: "auto_fix: {review: 0, test: 1, lint: 1, rebase: 1, ci: 1}\n", trusted: "agent: claude\nallow_repo_commands: true\n"},
		{name: "trusted command opt-in uses submitted codex", task: "agent: codex\nauto_fix: {review: 0, test: 1, lint: 1, rebase: 1, ci: 1}\n", trusted: "agent: claude\nallow_repo_commands: true\n"},
		{name: "trusted command opt-in rejects submitted claude", task: "agent: claude\nauto_fix: {review: 0, test: 1, lint: 1, rebase: 1, ci: 1}\n", trusted: "allow_repo_commands: true\n", wantErr: true},
		{name: "trusted command opt-in rejects submitted fallback", task: "agent: [codex, claude]\nauto_fix: {review: 0, test: 1, lint: 1, rebase: 1, ci: 1}\n", trusted: "allow_repo_commands: true\n", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader := Reader{Root: dir, Commands: &primaryRoutingRunner{task: []byte(test.task), trusted: []byte(test.trusted)}}
			_, err := reader.CheckStart(context.Background(), project, filepath.Join(project, "worktree"), "feature", "ship safely", versionFive())
			if test.wantErr {
				if err == nil || !strings.Contains(err.Error(), "agent overrides") {
					t.Fatalf("CheckStart error=%v, want trusted primary refusal", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("CheckStart: %v", err)
			}
		})
	}
}

// Under version 6 a run carries its own agent, which no-mistakes applies over
// whatever the repository names, so no repository agent stops a start, and
// the start hands back the trusted commit the launch selection names.
func TestCheckStartLeavesARepositoryAgentToTheLaunchSelection(t *testing.T) {
	sqlite, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	project := filepath.Join(t.TempDir(), "project")
	dir := t.TempDir()
	sql := `CREATE TABLE repos(id TEXT,working_path TEXT,default_branch TEXT);
CREATE TABLE runs(id TEXT,repo_id TEXT,branch TEXT,created_at INTEGER,status TEXT,head_sha TEXT,submitted_head_sha TEXT,intent TEXT);
INSERT INTO repos VALUES('repo',` + sqlString(filepath.ToSlash(project)) + `,'main');`
	if out, err := exec.Command(sqlite, filepath.Join(dir, "state.sqlite"), sql).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %s %v", out, err)
	}
	for _, trusted := range []string{"auto_fix: {review: 0}\n", "agent: codex\n", "agent: claude\n", "agent: [codex, claude]\n", "agent: auto\n"} {
		reader := Reader{Root: dir, Commands: &primaryRoutingRunner{trusted: []byte(trusted)}}
		check, err := reader.CheckStart(context.Background(), project, filepath.Join(project, "worktree"), "feature", "ship safely", versionSix(t, Reviewer{}))
		if err != nil || check.TrustedHead != primaryRoutingSHA {
			t.Fatalf("trusted %q: check=%+v err=%v, want the start allowed at the trusted commit", trusted, check, err)
		}
	}
}

// A live run keeps working after the native bounded wait expires, and the run
// it left behind is the same work when the branch still holds its submitted
// head and the intent is the one it was started with. Reattach to that run
// instead of refusing it as an earlier run that must be resolved (issue 1421).
func TestCheckStartReattachesToTheLiveRunOfTheSameWork(t *testing.T) {
	sqlite, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	const liveIntent = "ship the connection fix safely"
	const movedHead = "89abcdef0123456789abcdef0123456789abcdef"
	for _, c := range []struct {
		name           string
		submittedHead  string
		head           string
		intent         string
		wantReattach   string
		wantUnresolved bool
	}{
		{name: "same branch, head and intent", submittedHead: primaryRoutingSHA, wantReattach: "previous"},
		{name: "the intent names other work", submittedHead: primaryRoutingSHA, intent: "rework the billing export", wantUnresolved: true},
		{name: "the branch moved since the run started", submittedHead: primaryRoutingSHA, head: movedHead, wantUnresolved: true},
		{name: "a run that records no submitted head at the same head", wantReattach: "previous"},
		{name: "a run that records no submitted head at a moved head", head: movedHead, wantUnresolved: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			project := filepath.Join(t.TempDir(), "project")
			dir := t.TempDir()
			intent := c.intent
			if intent == "" {
				intent = liveIntent
			}
			sql := `CREATE TABLE repos(id TEXT,working_path TEXT,default_branch TEXT);
CREATE TABLE runs(id TEXT,repo_id TEXT,branch TEXT,created_at INTEGER,status TEXT,head_sha TEXT,submitted_head_sha TEXT,intent TEXT);
INSERT INTO repos VALUES('repo',` + sqlString(filepath.ToSlash(project)) + `,'main');
INSERT INTO runs VALUES('previous','repo','feature',1,'running','` + primaryRoutingSHA + `','` + c.submittedHead + `','` + liveIntent + `');`
			if out, err := exec.Command(sqlite, filepath.Join(dir, "state.sqlite"), sql).CombinedOutput(); err != nil {
				t.Fatalf("fixture: %s %v", out, err)
			}
			reader := Reader{Root: dir, Commands: &primaryRoutingRunner{head: c.head}}

			// Act
			check, err := reader.CheckStart(context.Background(), project, filepath.Join(project, "worktree"), "feature", intent, testPolicy(t))

			// Assert
			if c.wantUnresolved {
				if !errors.Is(err, ErrUnresolved) || check.ReattachRun != "" {
					t.Fatalf("check=%+v err=%v, want an unresolved earlier-run refusal", check, err)
				}
				return
			}
			// A reattachment carries no launch selection, so it names no
			// trusted commit.
			if err != nil || check.ReattachRun != c.wantReattach || check.TrustedHead != "" {
				t.Fatalf("check=%+v err=%v, want reattachment to %s with no trusted head", check, err, c.wantReattach)
			}
		})
	}
}

func TestCheckStartRefusesOnlyANonTerminalPreviousRun(t *testing.T) {
	sqlite, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	for _, c := range []struct {
		status     string
		unresolved bool
	}{
		{"completed", false},
		{"failed", false},
		{"cancelled", false},
		{"ci_monitor_interrupted", false},
		{"running", true},
		{"pending", true},
		{"awaiting_approval", true},
		{"unknown", true},
	} {
		t.Run(c.status, func(t *testing.T) {
			dir := t.TempDir()
			sql := `CREATE TABLE repos(id TEXT,working_path TEXT,default_branch TEXT);
CREATE TABLE runs(id TEXT,repo_id TEXT,branch TEXT,created_at INTEGER,status TEXT,head_sha TEXT,submitted_head_sha TEXT,intent TEXT);
INSERT INTO repos VALUES('repo','C:\project','main');
INSERT INTO runs VALUES('previous','repo','feat',1,'` + c.status + `','` + primaryRoutingSHA + `','` + primaryRoutingSHA + `','ship safely');`
			if out, err := exec.Command(sqlite, filepath.Join(dir, "state.sqlite"), sql).CombinedOutput(); err != nil {
				t.Fatalf("fixture: %s %v", out, err)
			}
			worktree := t.TempDir()
			// The worktree holds a commit of its own, so a live run is read as
			// other work than this branch: it started at the fixture's head and
			// this branch has moved past it. The search for a repository stops
			// above the worktree, not at whatever checkout holds the temp folder.
			runPipelineGit(t, worktree, "init", "--initial-branch=feat")
			runPipelineGit(t, worktree, "-c", "user.name=Pipeline Test", "-c", "user.email=pipeline@example.com", "commit", "--allow-empty", "-m", "work")
			t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(worktree))
			reader := Reader{Root: dir, Commands: fixtureCommands{execx.OSRunner{}}}
			_, err := reader.CheckStart(context.Background(), "C:/project", worktree, "feat", "ship safely", testPolicy(t))
			if errors.Is(err, ErrUnresolved) != c.unresolved {
				t.Fatalf("status %q: %v", c.status, err)
			}
		})
	}
}

// The native engine persists a disabled auto-fix budget as SQL NULL, never 0,
// so the decode-and-respond path has to accept a null limit or no real review
// gate can ever be answered.
func TestResponseAcceptsTheNullReviewLimitTheEngineActuallyWrites(t *testing.T) {
	sqlite, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	dir := t.TempDir()
	sql := `CREATE TABLE repos(id TEXT,working_path TEXT,default_branch TEXT);
CREATE TABLE runs(id TEXT,repo_id TEXT,branch TEXT,created_at INTEGER,status TEXT);
CREATE TABLE step_results(id TEXT,run_id TEXT,step_name TEXT,status TEXT,auto_fix_limit INTEGER,findings_json TEXT,step_order INTEGER);
CREATE TABLE step_rounds(step_result_id TEXT,round INTEGER,selection_source TEXT);
INSERT INTO repos VALUES('repo','C:\project','main');
INSERT INTO runs VALUES('disabled','repo','feat/disabled',1,'running'),('automatic','repo','feat/automatic',2,'running');
INSERT INTO step_results VALUES('disabledstep','disabled','review','awaiting_approval',NULL,'{"findings":[{"id":"bug","action":"auto-fix"}]}',1),('automaticstep','automatic','review','awaiting_approval',10,'{"findings":[{"id":"bug","action":"auto-fix"}]}',1);
INSERT INTO step_rounds VALUES('disabledstep',1,NULL),('automaticstep',1,NULL);`
	if out, err := exec.Command(sqlite, filepath.Join(dir, "state.sqlite"), sql).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %s %v", out, err)
	}
	selection, err := testPolicy(t).Select("ordinary")
	if err != nil {
		t.Fatal(err)
	}
	reader := Reader{Root: dir, Commands: fixtureCommands{execx.OSRunner{}}}
	gate, err := reader.Gate(context.Background(), "C:/project", "feat/disabled")
	if err != nil || gate.AutoFixLimit != nil {
		t.Fatalf("null limit did not decode as nil: %+v %v", gate, err)
	}
	args, err := ResponseArgs(selection, gate, Response{Action: "fix", Findings: "bug"})
	if err != nil {
		t.Fatalf("null review limit refused: %v", err)
	}
	want := "axi respond --step review --action fix --findings bug"
	if strings.Join(args, " ") != want {
		t.Fatalf("args: %q", strings.Join(args, " "))
	}
	// A live automatic budget still means the step is repairing itself, which
	// is the case the guard exists to refuse.
	automatic, err := reader.Gate(context.Background(), "C:/project", "feat/automatic")
	if err != nil || automatic.AutoFixLimit == nil || *automatic.AutoFixLimit != 10 {
		t.Fatalf("automatic gate: %+v %v", automatic, err)
	}
	if _, err := ResponseArgs(selection, automatic, Response{Action: "fix", Findings: "bug"}); err == nil {
		t.Fatal("automatic review budget accepted")
	}
}

// This repository's own committed .no-mistakes.yaml bounds every gate run of
// it, and CheckStart reads it through checkRepoConfig. Editing either
// checked-in artifact out of agreement would only surface as a refused gate
// run, so assert the real consumer accepts the real pair, and the version 6
// policy a home keeps until it moves to version 7.
func TestCommittedRepoConfigSatisfiesTheCheckedInPolicy(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", ".no-mistakes.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckRepoConfig(data, testPolicy(t)); err != nil {
		t.Fatalf("committed gate config conflicts with config/pipeline.json: %v", err)
	}
	if err := CheckRepoConfig(data, versionSix(t, Reviewer{})); err != nil {
		t.Fatalf("committed gate config conflicts with a version 6 home: %v", err)
	}
	var config struct {
		Agent yaml.Node `yaml:"agent"`
	}
	if err := yaml.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if config.Agent.Kind != 0 {
		t.Fatalf("committed config overrides the globally owned primary agent: %+v", config.Agent)
	}
}

const acceptedReattachmentHead = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
const releasedReattachmentHead = "cccccccccccccccccccccccccccccccccccccccc"

type acceptedReattachmentRunner struct {
	execx.Runner
	head                string
	branch              string
	isDirty             bool
	secondHead          string
	headReads           int
	receiptReads        int
	beforeReceiptRead   func()
	beforeReceiptReread func()
}

func (r *acceptedReattachmentRunner) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	if request.Name == "sqlite3" {
		if !slices.Contains(request.Args, "-readonly") {
			return execx.Result{}, errors.New("reattachment attempted a writable database command")
		}
		if strings.Contains(request.Args[len(request.Args)-1], "ci_handbacks") {
			r.receiptReads++
			if r.receiptReads == 1 && r.beforeReceiptRead != nil {
				r.beforeReceiptRead()
			}
			if r.receiptReads == 2 && r.beforeReceiptReread != nil {
				r.beforeReceiptReread()
			}
		}
		return r.Runner.Run(ctx, request)
	}
	if request.Name == "git" {
		switch strings.Join(request.Args, " ") {
		case "rev-parse HEAD":
			r.headReads++
			head := r.head
			if r.headReads > 1 && r.secondHead != "" {
				head = r.secondHead
			}
			return execx.Result{Stdout: []byte(head + "\n")}, nil
		case "symbolic-ref --quiet --short HEAD":
			return execx.Result{Stdout: []byte(r.branch + "\n")}, nil
		case "status --porcelain --untracked-files=all":
			if r.isDirty {
				return execx.Result{Stdout: []byte(" M source.go\n")}, nil
			}
			return execx.Result{}, nil
		}
	}
	return execx.Result{}, errors.New("unexpected reattachment command: " + request.Name + " " + strings.Join(request.Args, " "))
}

func acceptedReattachmentFixture(t *testing.T) (Reader, string, string, *acceptedReattachmentRunner) {
	t.Helper()
	project, worktree, root := t.TempDir(), t.TempDir(), t.TempDir()
	candidate := `{"Head":"` + acceptedReattachmentHead + `","BaseHead":"` + releasedReattachmentHead + `","ManagedBranch":"HEAD","UpstreamURL":"https://example.com/widget.git","ForkURL":"","ReviewRoundID":"review-1"}`
	sql := `CREATE TABLE repos(id TEXT,working_path TEXT,default_branch TEXT,upstream_url TEXT,fork_url TEXT);
CREATE TABLE runs(id TEXT,repo_id TEXT,branch TEXT,created_at INTEGER,status TEXT,head_sha TEXT,submitted_head_sha TEXT,intent TEXT,push_active INTEGER,custody_returned_at INTEGER,pr_url TEXT,pr_state TEXT,last_pushed_sha TEXT,push_generation INTEGER,push_target_fingerprint TEXT,push_ref TEXT);
CREATE TABLE ci_handbacks(id TEXT,run_id TEXT,round_id TEXT,caller_dir TEXT,released_head TEXT,pr_url TEXT,push_generation INTEGER,target_fingerprint TEXT,push_ref TEXT,accepted_head TEXT,accepted_at INTEGER,candidate_json TEXT);
INSERT INTO repos VALUES('repo-1',` + sqlString(filepath.ToSlash(project)) + `,'main','https://example.com/widget.git','');
INSERT INTO runs VALUES('run-1','repo-1','feature',1,'running','` + acceptedReattachmentHead + `','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','ship the correction',0,NULL,'https://example.com/widget/pull/1','open','` + releasedReattachmentHead + `',7,'target-1','refs/heads/feature');
INSERT INTO ci_handbacks VALUES('receipt-1','run-1','round-1',` + sqlString(worktree) + `,'` + releasedReattachmentHead + `','https://example.com/widget/pull/1',7,'target-1','refs/heads/feature','` + acceptedReattachmentHead + `',2,` + sqlString(candidate) + `);`
	updateReattachmentFixture(t, root, sql)
	runner := &acceptedReattachmentRunner{Runner: fixtureCommands{execx.OSRunner{}}, head: acceptedReattachmentHead, branch: "feature"}
	return Reader{Root: root, Commands: runner}, project, worktree, runner
}

func updateReattachmentFixture(t *testing.T, root, sql string) {
	t.Helper()
	sqlite, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Fatal("SQLite is required for receipt-bound reattachment fixtures")
	}
	if output, err := exec.Command(sqlite, filepath.Join(root, "state.sqlite"), sql).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %s %v", output, err)
	}
}

func TestCheckStartReattachesToAcceptedCIHandback(t *testing.T) {
	for _, test := range []struct {
		name string
		sql  string
	}{
		{name: "released publication"},
		{name: "accepted publication", sql: `UPDATE runs SET last_pushed_sha='` + acceptedReattachmentHead + `',push_generation=8;`},
		{name: "candidate names the managed branch", sql: `UPDATE ci_handbacks SET candidate_json=replace(candidate_json,'HEAD','feature');`},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			reader, project, worktree, _ := acceptedReattachmentFixture(t)
			if test.sql != "" {
				updateReattachmentFixture(t, reader.Root, test.sql)
			}
			before, err := os.ReadFile(filepath.Join(reader.Root, "state.sqlite"))
			if err != nil {
				t.Fatal(err)
			}

			// Act
			check, err := reader.CheckStart(t.Context(), project, worktree, "feature", "ship the correction", testPolicy(t))

			// Assert
			if err != nil || check.ReattachRun != "run-1" || check.TrustedHead != "" {
				t.Fatalf("got %+v, %v; want the accepted original run without a new launch", check, err)
			}
			after, err := os.ReadFile(filepath.Join(reader.Root, "state.sqlite"))
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("reattachment changed fixture state: %v", err)
			}
		})
	}
}

func TestCheckStartRefusesInvalidCIHandbackReattachment(t *testing.T) {
	for _, test := range []struct {
		name   string
		sql    string
		intent string
		change func(*acceptedReattachmentRunner)
	}{
		{name: "no receipt", sql: `DELETE FROM ci_handbacks;`},
		{name: "latest receipt is unaccepted", sql: `INSERT INTO ci_handbacks SELECT 'receipt-2',run_id,round_id,caller_dir,released_head,pr_url,push_generation,target_fingerprint,push_ref,NULL,NULL,NULL FROM ci_handbacks;`},
		{name: "foreign run", sql: `UPDATE ci_handbacks SET run_id='other-run';`},
		{name: "foreign repository", sql: `UPDATE runs SET repo_id='other-repo';`},
		{name: "malformed repository identity", sql: `UPDATE repos SET id='invalid repo'; UPDATE runs SET repo_id='invalid repo';`},
		{name: "foreign branch", sql: `UPDATE runs SET branch='other-branch';`},
		{name: "foreign caller", sql: `UPDATE ci_handbacks SET caller_dir='C:/another-caller';`},
		{name: "wrong symbolic branch", change: func(r *acceptedReattachmentRunner) { r.branch = "other-branch" }},
		{name: "dirty caller", change: func(r *acceptedReattachmentRunner) { r.isDirty = true }},
		{name: "caller moved", change: func(r *acceptedReattachmentRunner) { r.head = releasedReattachmentHead }},
		{name: "caller moves during check", change: func(r *acceptedReattachmentRunner) { r.secondHead = releasedReattachmentHead }},
		{name: "current head differs", sql: `UPDATE runs SET head_sha='` + releasedReattachmentHead + `';`},
		{name: "later native descendant", sql: `UPDATE runs SET head_sha='dddddddddddddddddddddddddddddddddddddddd';`, change: func(r *acceptedReattachmentRunner) { r.head = "dddddddddddddddddddddddddddddddddddddddd" }},
		{name: "accepted head differs", sql: `UPDATE ci_handbacks SET accepted_head='` + releasedReattachmentHead + `';`},
		{name: "candidate head differs", sql: `UPDATE ci_handbacks SET candidate_json=replace(candidate_json,'` + acceptedReattachmentHead + `','dddddddddddddddddddddddddddddddddddddddd');`},
		{name: "malformed candidate", sql: `UPDATE ci_handbacks SET candidate_json='{';`},
		{name: "missing candidate", sql: `UPDATE ci_handbacks SET candidate_json=NULL;`},
		{name: "missing checkpoint", sql: `UPDATE ci_handbacks SET candidate_json=replace(candidate_json,'review-1','');`},
		{name: "malformed base hash", sql: `UPDATE ci_handbacks SET candidate_json=replace(candidate_json,'` + releasedReattachmentHead + `','short');`},
		{name: "wrong managed branch", sql: `UPDATE ci_handbacks SET candidate_json=replace(candidate_json,'HEAD','other-branch');`},
		{name: "malformed released hash", sql: `UPDATE ci_handbacks SET released_head='short';`},
		{name: "malformed accepted hash", sql: `UPDATE ci_handbacks SET accepted_head='short';`},
		{name: "missing acceptance time", sql: `UPDATE ci_handbacks SET accepted_at=NULL;`},
		{name: "invalid acceptance time", sql: `UPDATE ci_handbacks SET accepted_at=0;`},
		{name: "malformed receipt identity", sql: `UPDATE ci_handbacks SET id='invalid receipt';`},
		{name: "missing release round", sql: `UPDATE ci_handbacks SET round_id=NULL;`},
		{name: "ambiguous receipt", sql: `INSERT INTO ci_handbacks SELECT * FROM ci_handbacks;`},
		{name: "blank intent", intent: " "},
		{name: "changed intent", intent: "another task"},
		{name: "newer run", sql: `INSERT INTO runs SELECT 'run-2',repo_id,branch,2,status,head_sha,submitted_head_sha,intent,push_active,custody_returned_at,pr_url,pr_state,last_pushed_sha,push_generation,push_target_fingerprint,push_ref FROM runs;`},
		{name: "unknown status", sql: `UPDATE runs SET status='unknown';`},
		{name: "pending status", sql: `UPDATE runs SET status='pending';`},
		{name: "returned custody", sql: `UPDATE runs SET custody_returned_at=0;`},
		{name: "active publication", sql: `UPDATE runs SET push_active=1;`},
		{name: "unknown publication activity", sql: `UPDATE runs SET push_active=NULL;`},
		{name: "closed PR", sql: `UPDATE runs SET pr_state='closed';`},
		{name: "foreign PR", sql: `UPDATE ci_handbacks SET pr_url='https://example.com/widget/pull/2';`},
		{name: "missing PR", sql: `UPDATE runs SET pr_url=NULL;`},
		{name: "missing PR on both sides", sql: `UPDATE runs SET pr_url=NULL; UPDATE ci_handbacks SET pr_url=NULL;`},
		{name: "wrong ref", sql: `UPDATE runs SET push_ref='refs/heads/other';`},
		{name: "receipt ref differs", sql: `UPDATE ci_handbacks SET push_ref='refs/heads/other';`},
		{name: "foreign ref on both sides", sql: `UPDATE runs SET push_ref='refs/heads/other'; UPDATE ci_handbacks SET push_ref='refs/heads/other';`},
		{name: "empty target", sql: `UPDATE runs SET push_target_fingerprint='';`},
		{name: "empty target on both sides", sql: `UPDATE runs SET push_target_fingerprint=''; UPDATE ci_handbacks SET target_fingerprint='';`},
		{name: "target differs", sql: `UPDATE ci_handbacks SET target_fingerprint='target-2';`},
		{name: "upstream differs", sql: `UPDATE repos SET upstream_url='https://example.com/other.git';`},
		{name: "empty upstream on both sides", sql: `UPDATE repos SET upstream_url=''; UPDATE ci_handbacks SET candidate_json=replace(candidate_json,'https://example.com/widget.git','');`},
		{name: "fork differs", sql: `UPDATE repos SET fork_url='https://example.com/fork.git';`},
		{name: "missing publication", sql: `UPDATE runs SET last_pushed_sha=NULL;`},
		{name: "unrelated publication", sql: `UPDATE runs SET last_pushed_sha='dddddddddddddddddddddddddddddddddddddddd';`},
		{name: "missing receipt generation", sql: `UPDATE ci_handbacks SET push_generation=0;`},
		{name: "missing generation on both sides", sql: `UPDATE runs SET push_generation=0; UPDATE ci_handbacks SET push_generation=0;`},
		{name: "released publication generation advanced", sql: `UPDATE runs SET push_generation=8;`},
		{name: "accepted publication generation unchanged", sql: `UPDATE runs SET last_pushed_sha='` + acceptedReattachmentHead + `';`},
		{name: "publication generation jumped", sql: `UPDATE runs SET last_pushed_sha='` + acceptedReattachmentHead + `',push_generation=9;`},
		{name: "receipt schema unavailable", sql: `DROP TABLE ci_handbacks;`},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			reader, project, worktree, runner := acceptedReattachmentFixture(t)
			if test.sql != "" {
				updateReattachmentFixture(t, reader.Root, test.sql)
			}
			if test.change != nil {
				test.change(runner)
			}
			intent := test.intent
			if intent == "" {
				intent = "ship the correction"
			}
			before, err := os.ReadFile(filepath.Join(reader.Root, "state.sqlite"))
			if err != nil {
				t.Fatal(err)
			}

			// Act
			check, err := reader.CheckStart(t.Context(), project, worktree, "feature", intent, testPolicy(t))

			// Assert
			if err == nil || check != (StartCheck{}) {
				t.Fatalf("got %+v, %v; want refusal without a launch or reattachment", check, err)
			}
			after, readErr := os.ReadFile(filepath.Join(reader.Root, "state.sqlite"))
			if readErr != nil || !bytes.Equal(before, after) {
				t.Fatalf("refusal changed fixture state: %v", readErr)
			}
		})
	}
	t.Run("binding changes during check", func(t *testing.T) {
		reader, project, worktree, runner := acceptedReattachmentFixture(t)
		runner.beforeReceiptReread = func() {
			updateReattachmentFixture(t, reader.Root, `UPDATE runs SET push_active=1;`)
		}
		check, err := reader.CheckStart(t.Context(), project, worktree, "feature", "ship the correction", testPolicy(t))
		if !errors.Is(err, ErrUnresolved) || check != (StartCheck{}) || !strings.Contains(err.Error(), "binding changed") {
			t.Fatalf("got %+v, %v; want identity drift refusal", check, err)
		}
	})
	// The run row changes between the previous-run query and the first receipt
	// read, so only the receipt's comparison with that earlier run can refuse it.
	for _, test := range []struct {
		name   string
		sql    string
		racing string
	}{
		{name: "run turns running before receipt read", sql: `UPDATE runs SET status='pending';`, racing: `UPDATE runs SET status='running';`},
		{name: "run head changes before receipt read", sql: `UPDATE runs SET head_sha='dddddddddddddddddddddddddddddddddddddddd';`, racing: `UPDATE runs SET head_sha='` + acceptedReattachmentHead + `';`},
		{name: "submitted head changes before receipt read", sql: `UPDATE runs SET submitted_head_sha='eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee';`, racing: `UPDATE runs SET submitted_head_sha='aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';`},
		{name: "run intent changes before receipt read", racing: `UPDATE runs SET intent='another task';`},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			reader, project, worktree, runner := acceptedReattachmentFixture(t)
			if test.sql != "" {
				updateReattachmentFixture(t, reader.Root, test.sql)
			}
			runner.beforeReceiptRead = func() { updateReattachmentFixture(t, reader.Root, test.racing) }

			// Act
			check, err := reader.CheckStart(t.Context(), project, worktree, "feature", "ship the correction", testPolicy(t))

			// Assert
			if !errors.Is(err, ErrUnresolved) || check != (StartCheck{}) || !strings.Contains(err.Error(), "run identity or custody") {
				t.Fatalf("got %+v, %v; want the run identity refusal", check, err)
			}
		})
	}
	// The caller changes only after the receipt re-read, so only the final
	// caller check can refuse it.
	for _, test := range []struct {
		name   string
		change func(*acceptedReattachmentRunner)
		want   string
	}{
		{name: "caller moves after receipt re-read", change: func(r *acceptedReattachmentRunner) { r.head = releasedReattachmentHead }, want: "caller head changed"},
		{name: "caller turns dirty after receipt re-read", change: func(r *acceptedReattachmentRunner) { r.isDirty = true }, want: "clean caller worktree"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			reader, project, worktree, runner := acceptedReattachmentFixture(t)
			runner.beforeReceiptReread = func() { test.change(runner) }

			// Act
			check, err := reader.CheckStart(t.Context(), project, worktree, "feature", "ship the correction", testPolicy(t))

			// Assert
			if !errors.Is(err, ErrUnresolved) || check != (StartCheck{}) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %+v, %v; want a final caller recheck refusal saying %q", check, err, test.want)
			}
		})
	}
	for _, status := range []string{"completed", "failed", "cancelled", "ci_monitor_interrupted"} {
		t.Run("terminal "+status, func(t *testing.T) {
			reader, project, worktree, runner := acceptedReattachmentFixture(t)
			updateReattachmentFixture(t, reader.Root, `UPDATE runs SET status=`+sqlString(status)+`;`)
			check, _ := reader.CheckStart(t.Context(), project, worktree, "feature", "ship the correction", testPolicy(t))
			if check.ReattachRun != "" || runner.receiptReads != 0 {
				t.Fatalf("terminal run used accepted receipt: %+v, %d reads", check, runner.receiptReads)
			}
		})
	}
}
