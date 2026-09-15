package pipeline

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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
	reader := Reader{Root: dir, Commands: execx.OSRunner{}}
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

func TestRepoPolicyCannotRaiseCapsAndDoesNotOwnReviewAgents(t *testing.T) {
	p := testPolicy(t)
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

func TestRepoAgentCannotAlterRenderedGlobalReviewAgents(t *testing.T) {
	p := testPolicy(t)
	for _, source := range []string{"agent: claude", "agent: codex", "agent: [claude, codex]"} {
		if err := CheckRepoConfig([]byte(source), p); err != nil {
			t.Fatalf("repository agent refused: %s: %v", source, err)
		}
		rendered, _, err := Render([]byte(source+"\nreview_agents:\n  reviewer: {agent: claude}\n  fixer: {agent: claude}\n"), p)
		if err != nil {
			t.Fatal(err)
		}
		var config struct {
			ReviewAgents map[string]struct {
				Agent  string `yaml:"agent"`
				Model  string `yaml:"model"`
				Effort string `yaml:"effort"`
			} `yaml:"review_agents"`
		}
		if err := yaml.Unmarshal(rendered, &config); err != nil {
			t.Fatal(err)
		}
		for _, role := range []string{"reviewer", "fixer"} {
			profile := config.ReviewAgents[role]
			if profile.Agent != "codex" || profile.Model != "gpt-5.6-sol" || profile.Effort != "high" {
				t.Fatalf("repository %q changed global %s profile: %+v", source, role, profile)
			}
		}
	}
}

type primaryRoutingRunner struct {
	task    []byte
	trusted []byte
}

const primaryRoutingSHA = "0123456789abcdef0123456789abcdef01234567"

func (r *primaryRoutingRunner) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	if request.Name == "sqlite3" {
		return (execx.OSRunner{}).Run(ctx, request)
	}
	if request.Name != "git" {
		return execx.Result{}, errors.New("unexpected command")
	}
	switch strings.Join(request.Args, " ") {
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
		return execx.Result{Stdout: r.trusted}, nil
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
CREATE TABLE runs(id TEXT,repo_id TEXT,branch TEXT,created_at INTEGER,status TEXT);
INSERT INTO repos VALUES('repo',` + sqlString(filepath.ToSlash(project)) + `,'main');`
	if out, err := exec.Command("sqlite3", filepath.Join(state, "state.sqlite"), sql).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %s %v", out, err)
	}
	reader := Reader{Root: state, Commands: execx.OSRunner{}}
	err := reader.CheckStart(context.Background(), project, project, "feature", testPolicy(t))
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
CREATE TABLE runs(id TEXT,repo_id TEXT,branch TEXT,created_at INTEGER,status TEXT);
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
			err := reader.CheckStart(context.Background(), project, filepath.Join(project, "worktree"), "feature", testPolicy(t))
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

func TestCheckStartRefusesUnboundHistoryIncludingTerminalRuns(t *testing.T) {
	sqlite, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	for _, c := range []struct {
		status     string
		unresolved bool
	}{
		{"completed", true},
		{"failed", true},
		{"cancelled", true},
		{"running", true},
		{"awaiting_approval", true},
	} {
		t.Run(c.status, func(t *testing.T) {
			dir := t.TempDir()
			sql := `CREATE TABLE repos(id TEXT,working_path TEXT,default_branch TEXT);
CREATE TABLE runs(id TEXT,repo_id TEXT,branch TEXT,created_at INTEGER,status TEXT);
INSERT INTO repos VALUES('repo','C:\project','main');
INSERT INTO runs VALUES('previous','repo','feat',1,'` + c.status + `');`
			if out, err := exec.Command(sqlite, filepath.Join(dir, "state.sqlite"), sql).CombinedOutput(); err != nil {
				t.Fatalf("fixture: %s %v", out, err)
			}
			reader := Reader{Root: dir, Commands: execx.OSRunner{}}
			err := reader.CheckStart(context.Background(), "C:/project", t.TempDir(), "feat", testPolicy(t))
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
	reader := Reader{Root: dir, Commands: execx.OSRunner{}}
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

// This repository's own committed .no-mistakes.yaml is what bounds the initial
// rollout gate, and CheckStart reads it through checkRepoConfig. Editing either
// checked-in artifact out of agreement would only surface as a refused gate
// run, so assert the real consumer accepts the real pair.
func TestCommittedRepoConfigSatisfiesTheCheckedInPolicy(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", ".no-mistakes.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckRepoConfig(data, testPolicy(t)); err != nil {
		t.Fatalf("committed gate config conflicts with config/pipeline.json: %v", err)
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
