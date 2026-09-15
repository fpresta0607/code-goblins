package pipeline

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"gopkg.in/yaml.v3"
)

type nativeAgentRaceRunner struct {
	inner      execx.OSRunner
	database   string
	trustedSHA string
	mutated    bool
}

type nativeWorktreeRaceRunner struct {
	inner      execx.OSRunner
	worktree   string
	trustedSHA string
	targetSHA  string
	mutated    bool
}

func (r *nativeWorktreeRaceRunner) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	result, err := r.inner.Run(ctx, request)
	if !r.mutated && request.Name == "git" && strings.Join(request.Args, " ") == "show "+r.trustedSHA+":.no-mistakes.yaml" {
		r.mutated = true
		if out, checkoutErr := exec.Command("git", "-C", r.worktree, "checkout", "--detach", r.targetSHA).CombinedOutput(); checkoutErr != nil {
			return execx.Result{}, fmt.Errorf("worktree race fixture: %s: %w", out, checkoutErr)
		}
	}
	return result, err
}

func (r *nativeAgentRaceRunner) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	if !r.mutated && request.Name == "git" && strings.Join(request.Args, " ") == "show "+r.trustedSHA+":.no-mistakes.yaml" {
		r.mutated = true
		if out, err := exec.Command("sqlite3", r.database, `UPDATE runs SET status='cancelled' WHERE id='run'`).CombinedOutput(); err != nil {
			return execx.Result{}, fmt.Errorf("race fixture: %s: %w", out, err)
		}
	}
	return r.inner.Run(ctx, request)
}

func TestVerifyNativeAgentBindsDurableRunAndImmutableGitEvidence(t *testing.T) {
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
	nativeWorktree := filepath.Join(root, "native")
	nativeRoot := filepath.Join(root, "native-state")
	for _, dir := range []string{remote, seed, nativeRoot} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	runPipelineGit(t, remote, "init", "--bare", "--initial-branch=main")
	runPipelineGit(t, seed, "init", "--initial-branch=main")
	runPipelineGit(t, seed, "config", "user.name", "Pipeline Test")
	runPipelineGit(t, seed, "config", "user.email", "pipeline@example.com")
	trustedConfig := []byte("auto_fix: {review: 0, test: 1, lint: 1, rebase: 1, ci: 1}\n")
	if err := os.WriteFile(filepath.Join(seed, ".no-mistakes.yaml"), trustedConfig, 0o600); err != nil {
		t.Fatal(err)
	}
	runPipelineGit(t, seed, "add", ".no-mistakes.yaml")
	runPipelineGit(t, seed, "commit", "-m", "trusted")
	runPipelineGit(t, seed, "remote", "add", "origin", remote)
	runPipelineGit(t, seed, "push", "-u", "origin", "main")
	runPipelineGit(t, root, "clone", remote, project)
	runPipelineGit(t, project, "config", "user.name", "Pipeline Test")
	runPipelineGit(t, project, "config", "user.email", "pipeline@example.com")
	runPipelineGit(t, project, "switch", "-c", "feature")
	if err := os.WriteFile(filepath.Join(project, "feature.txt"), []byte("feature\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runPipelineGit(t, project, "add", "feature.txt")
	runPipelineGit(t, project, "commit", "-m", "feature")
	submitted := runPipelineGit(t, project, "rev-parse", "HEAD")
	runPipelineGit(t, project, "push", "-u", "origin", "feature")
	trusted := runPipelineGit(t, project, "rev-parse", "refs/remotes/origin/main")
	runPipelineGit(t, project, "switch", "main")
	runPipelineGit(t, root, "clone", remote, nativeWorktree)
	runPipelineGit(t, nativeWorktree, "switch", "feature")
	globalConfig := []byte("agent: [codex]\n")
	if err := os.WriteFile(filepath.Join(nativeRoot, "config.yaml"), globalConfig, 0o600); err != nil {
		t.Fatal(err)
	}
	sql := `CREATE TABLE repos(id TEXT,working_path TEXT,default_branch TEXT);
CREATE TABLE runs(id TEXT,repo_id TEXT,branch TEXT,head_sha TEXT,submitted_head_sha TEXT,status TEXT,created_at INTEGER,launch_nonce TEXT,launch_validation_generation TEXT,worktree_dir TEXT,no_mistakes_version TEXT,no_mistakes_build_sha TEXT);
CREATE TABLE step_results(id TEXT,run_id TEXT,step_name TEXT,status TEXT,completed_at INTEGER);
CREATE TABLE step_rounds(id TEXT,step_result_id TEXT,round INTEGER,starting_head_sha TEXT,reviewed_head_sha TEXT,created_at INTEGER);
CREATE TABLE agent_invocations(run_id TEXT,step_name TEXT,started_at INTEGER);
CREATE TABLE uncertified_pipeline_ranges(repo_id TEXT,branch TEXT,from_sha TEXT,to_sha TEXT,source_run_id TEXT,created_at INTEGER);
INSERT INTO repos VALUES('repo',` + sqlString(filepath.ToSlash(project)) + `,'main');
INSERT INTO runs VALUES('run','repo','feature','` + submitted + `','` + submitted + `','running',1,'nonce','generation',` + sqlString(filepath.ToSlash(nativeWorktree)) + `,'v1.75.1','37ed232');`
	if out, err := exec.Command("sqlite3", filepath.Join(nativeRoot, "state.sqlite"), sql).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %s %v", out, err)
	}
	want := NativeAgentExpectation{
		RunID: "run", Project: project, RepoID: "repo", Branch: "feature", SubmittedHeadSHA: submitted,
		LaunchNonce: "nonce", ValidationGeneration: "generation", DefaultBranch: "main", TrustedSHA: trusted,
		TaskConfigSHA256: fmt.Sprintf("%x", sha256.Sum256(trustedConfig)), TrustedConfigSHA256: fmt.Sprintf("%x", sha256.Sum256(trustedConfig)),
		GlobalConfigSHA256: fmt.Sprintf("%x", sha256.Sum256(globalConfig)), EffectivePrimary: "codex",
	}
	reader := Reader{Root: nativeRoot, Commands: execx.OSRunner{}}
	if err := reader.VerifyNativeAgent(context.Background(), nativeWorktree, want); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("sqlite3", filepath.Join(nativeRoot, "state.sqlite"), `UPDATE runs SET no_mistakes_version='other'`).CombinedOutput(); err != nil {
		t.Fatalf("version fixture: %s %v", out, err)
	}
	if err := reader.VerifyNativeAgent(context.Background(), nativeWorktree, want); err == nil || !strings.Contains(err.Error(), "version") {
		t.Fatalf("pre-agent native version mismatch error=%v", err)
	}
	if out, err := exec.Command("sqlite3", filepath.Join(nativeRoot, "state.sqlite"), `UPDATE runs SET no_mistakes_version='v1.75.1'`).CombinedOutput(); err != nil {
		t.Fatalf("restore version fixture: %s %v", out, err)
	}
	if err := os.WriteFile(filepath.Join(nativeWorktree, "fixed.txt"), []byte("fixed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runPipelineGit(t, nativeWorktree, "config", "user.name", "Pipeline Test")
	runPipelineGit(t, nativeWorktree, "config", "user.email", "pipeline@example.com")
	runPipelineGit(t, nativeWorktree, "add", "fixed.txt")
	runPipelineGit(t, nativeWorktree, "commit", "-m", "native fix")
	fixed := runPipelineGit(t, nativeWorktree, "rev-parse", "HEAD")
	if out, err := exec.Command("sqlite3", filepath.Join(nativeRoot, "state.sqlite"), `UPDATE runs SET head_sha=`+sqlString(fixed)+`; INSERT INTO uncertified_pipeline_ranges VALUES('repo','feature',`+sqlString(submitted)+`,`+sqlString(fixed)+`,'run',5)`).CombinedOutput(); err != nil {
		t.Fatalf("move durable head fixture: %s %v", out, err)
	}
	if err := reader.VerifyNativeAgent(context.Background(), nativeWorktree, want); err != nil {
		t.Fatalf("native-authorized descendant head refused: %v", err)
	}
	runPipelineGit(t, nativeWorktree, "checkout", "--detach", trusted)
	if out, err := exec.Command("sqlite3", filepath.Join(nativeRoot, "state.sqlite"), `DELETE FROM uncertified_pipeline_ranges; UPDATE runs SET head_sha=`+sqlString(trusted)+`; INSERT INTO step_results VALUES('review-step','run','review','completed',10); INSERT INTO step_rounds VALUES('pre-rebase-round','review-step',1,`+sqlString(trusted)+`,`+sqlString(trusted)+`,10)`).CombinedOutput(); err != nil {
		t.Fatalf("move unrelated durable head fixture: %s %v", out, err)
	}
	if err := reader.VerifyNativeAgent(context.Background(), nativeWorktree, want); err == nil || !strings.Contains(err.Error(), "authorized transition") {
		t.Fatalf("unbound durable and worktree head error=%v", err)
	}
	if err := os.WriteFile(filepath.Join(nativeWorktree, "rebased.txt"), []byte("rebased\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runPipelineGit(t, nativeWorktree, "add", "rebased.txt")
	runPipelineGit(t, nativeWorktree, "commit", "-m", "native rebase result")
	rebased := runPipelineGit(t, nativeWorktree, "rev-parse", "HEAD")
	if out, err := exec.Command("sqlite3", filepath.Join(nativeRoot, "state.sqlite"), `UPDATE runs SET head_sha=`+sqlString(rebased)+`; INSERT INTO step_results VALUES('rebase-step','run','rebase','completed',20)`).CombinedOutput(); err != nil {
		t.Fatalf("rebase transition fixture: %s %v", out, err)
	}
	if err := reader.VerifyNativeAgent(context.Background(), nativeWorktree, want); err != nil {
		t.Fatalf("completed native rebase head refused: %v", err)
	}
	if out, err := exec.Command("sqlite3", filepath.Join(nativeRoot, "state.sqlite"), `INSERT INTO uncertified_pipeline_ranges VALUES('repo','feature',`+sqlString(trusted)+`,`+sqlString(rebased)+`,'run',20)`).CombinedOutput(); err != nil {
		t.Fatalf("mismatched rebase range fixture: %s %v", out, err)
	}
	if err := reader.VerifyNativeAgent(context.Background(), nativeWorktree, want); err == nil || !strings.Contains(err.Error(), "authorized transition") {
		t.Fatalf("mismatched first-rebase transition error=%v", err)
	}
	if out, err := exec.Command("sqlite3", filepath.Join(nativeRoot, "state.sqlite"), `DELETE FROM uncertified_pipeline_ranges`).CombinedOutput(); err != nil {
		t.Fatalf("clear mismatched rebase range fixture: %s %v", out, err)
	}
	if out, err := exec.Command("sqlite3", filepath.Join(nativeRoot, "state.sqlite"), `INSERT INTO agent_invocations VALUES('run','review',21); UPDATE runs SET head_sha=`+sqlString(trusted)).CombinedOutput(); err != nil {
		t.Fatalf("review after rebase fixture: %s %v", out, err)
	}
	runPipelineGit(t, nativeWorktree, "checkout", "--detach", trusted)
	if err := reader.VerifyNativeAgent(context.Background(), nativeWorktree, want); err == nil || !strings.Contains(err.Error(), "authorized transition") {
		t.Fatalf("pre-rebase round authorized unrelated head: %v", err)
	}
	runPipelineGit(t, nativeWorktree, "checkout", "--detach", rebased)
	if out, err := exec.Command("sqlite3", filepath.Join(nativeRoot, "state.sqlite"), `UPDATE runs SET head_sha=`+sqlString(rebased)+`; INSERT INTO step_rounds VALUES('post-rebase-round','review-step',2,`+sqlString(rebased)+`,`+sqlString(rebased)+`,22)`).CombinedOutput(); err != nil {
		t.Fatalf("post-rebase review fixture: %s %v", out, err)
	}
	if err := reader.VerifyNativeAgent(context.Background(), nativeWorktree, want); err != nil {
		t.Fatalf("completed rebase binding expired after review: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nativeWorktree, "fix-one.txt"), []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runPipelineGit(t, nativeWorktree, "add", "fix-one.txt")
	runPipelineGit(t, nativeWorktree, "commit", "-m", "first chained fix")
	fixOne := runPipelineGit(t, nativeWorktree, "rev-parse", "HEAD")
	if out, err := exec.Command("sqlite3", filepath.Join(nativeRoot, "state.sqlite"), `UPDATE runs SET head_sha=`+sqlString(fixOne)+`; INSERT INTO step_rounds VALUES('fix-round','review-step',3,`+sqlString(rebased)+`,`+sqlString(fixOne)+`,23)`).CombinedOutput(); err != nil {
		t.Fatalf("first fixer transition fixture: %s %v", out, err)
	}
	if err := reader.VerifyNativeAgent(context.Background(), nativeWorktree, want); err != nil {
		t.Fatalf("review fixer transition refused: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nativeWorktree, "fix-two.txt"), []byte("two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runPipelineGit(t, nativeWorktree, "add", "fix-two.txt")
	runPipelineGit(t, nativeWorktree, "commit", "-m", "second chained fix")
	fixTwo := runPipelineGit(t, nativeWorktree, "rev-parse", "HEAD")
	if out, err := exec.Command("sqlite3", filepath.Join(nativeRoot, "state.sqlite"), `UPDATE runs SET head_sha=`+sqlString(fixTwo)+`; INSERT INTO uncertified_pipeline_ranges VALUES('repo','feature',`+sqlString(rebased)+`,`+sqlString(fixTwo)+`,'run',24)`).CombinedOutput(); err != nil {
		t.Fatalf("second fixer range fixture: %s %v", out, err)
	}
	if err := reader.VerifyNativeAgent(context.Background(), nativeWorktree, want); err != nil {
		t.Fatalf("chained fixer range refused: %v", err)
	}
	runPipelineGit(t, nativeWorktree, "checkout", "--detach", submitted)
	if out, err := exec.Command("sqlite3", filepath.Join(nativeRoot, "state.sqlite"), `UPDATE runs SET head_sha=`+sqlString(submitted)).CombinedOutput(); err != nil {
		t.Fatalf("restore durable head fixture: %s %v", out, err)
	}
	if err := os.WriteFile(filepath.Join(nativeRoot, "config.yaml"), []byte("agent: [claude]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := reader.VerifyNativeAgent(context.Background(), nativeWorktree, want); err == nil || !strings.Contains(err.Error(), "shared native config changed") {
		t.Fatalf("mutated shared config error=%v", err)
	}
	if err := os.WriteFile(filepath.Join(nativeRoot, "config.yaml"), globalConfig, 0o600); err != nil {
		t.Fatal(err)
	}
	runPipelineGit(t, nativeWorktree, "update-ref", "refs/remotes/origin/main", submitted)
	if err := reader.VerifyNativeAgent(context.Background(), nativeWorktree, want); err == nil || !strings.Contains(err.Error(), "tracking evidence changed") {
		t.Fatalf("mutated native tracking ref error=%v", err)
	}
	runPipelineGit(t, nativeWorktree, "update-ref", "refs/remotes/origin/main", trusted)
	worktreeRace := &nativeWorktreeRaceRunner{worktree: nativeWorktree, trustedSHA: trusted, targetSHA: fixed}
	reader.Commands = worktreeRace
	if err := reader.VerifyNativeAgent(context.Background(), nativeWorktree, want); err == nil || !worktreeRace.mutated || !strings.Contains(err.Error(), "worktree") {
		t.Fatalf("late worktree race error=%v mutated=%t", err, worktreeRace.mutated)
	}
	runPipelineGit(t, nativeWorktree, "checkout", "--detach", submitted)
	race := &nativeAgentRaceRunner{database: filepath.Join(nativeRoot, "state.sqlite"), trustedSHA: trusted}
	reader.Commands = race
	if err := reader.VerifyNativeAgent(context.Background(), nativeWorktree, want); err == nil || !race.mutated {
		t.Fatalf("database race error=%v mutated=%t", err, race.mutated)
	}
}

func TestVerifyNativeLaunchRequiresDurableModelAndGlobalConfig(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	root := t.TempDir()
	project := t.TempDir()
	nativeWorktree := filepath.Join(root, "worktrees", "run")
	globalConfig := []byte("agent: [codex]\nagent_config:\n  codex: {model: gpt-5.6-sol, effort: high}\n")
	sql := `CREATE TABLE repos(id TEXT,working_path TEXT,default_branch TEXT);
CREATE TABLE runs(id TEXT,repo_id TEXT,branch TEXT,submitted_head_sha TEXT,launch_nonce TEXT,launch_validation_generation TEXT,no_mistakes_version TEXT,no_mistakes_build_sha TEXT,worktree_dir TEXT);
CREATE TABLE step_results(id TEXT,run_id TEXT,step_name TEXT);
CREATE TABLE step_rounds(id TEXT,step_result_id TEXT,round INTEGER,trusted_config_sha TEXT,global_config_yaml BLOB,created_at INTEGER);
CREATE TABLE agent_invocations(id TEXT,run_id TEXT,step_name TEXT,agent TEXT,model TEXT,model_provider TEXT,started_at INTEGER);
INSERT INTO repos VALUES('repo',` + sqlString(filepath.ToSlash(project)) + `,'main');
INSERT INTO runs VALUES('run','repo','feature','submitted','nonce','generation','v1.75.1','37ed232',` + sqlString(filepath.ToSlash(nativeWorktree)) + `);
INSERT INTO step_results VALUES('review-step','run','review');
INSERT INTO step_rounds VALUES('round','review-step',1,'trusted',X'` + fmt.Sprintf("%x", globalConfig) + `',1);
INSERT INTO agent_invocations VALUES('invocation','run','review','codex','gpt-5.6-sol','openai',1);`
	database := filepath.Join(root, "state.sqlite")
	if out, err := exec.Command("sqlite3", database, sql).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %s %v", out, err)
	}
	want := NativeLaunchExpectation{
		RunID: "run", Project: project, RepoID: "repo", Branch: "feature", SubmittedHeadSHA: "submitted",
		LaunchNonce: "nonce", ValidationGeneration: "generation", TrustedSHA: "trusted", Primary: "codex",
		PrimaryModel: "gpt-5.6-sol", GlobalConfigSHA256: fmt.Sprintf("%x", sha256.Sum256(globalConfig)),
	}
	reader := Reader{Root: root, Commands: execx.OSRunner{}}
	state, err := reader.VerifyNativeLaunch(context.Background(), want)
	if err != nil || state.InvocationCount != 1 || !state.ReviewProvenance || !samePath(state.Worktree, nativeWorktree) {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	if out, err := exec.Command("sqlite3", database, `UPDATE runs SET no_mistakes_build_sha='other'`).CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %s %v", out, err)
	}
	if _, err := reader.VerifyNativeLaunch(context.Background(), want); err == nil || !strings.Contains(err.Error(), "version") {
		t.Fatalf("durable build mismatch error=%v", err)
	}
	if out, err := exec.Command("sqlite3", database, `UPDATE runs SET no_mistakes_build_sha='37ed232'`).CombinedOutput(); err != nil {
		t.Fatalf("restore build fixture: %s %v", out, err)
	}
	if out, err := exec.Command("sqlite3", database, `UPDATE agent_invocations SET model='other'`).CombinedOutput(); err != nil {
		t.Fatalf("model fixture: %s %v", out, err)
	}
	if _, err := reader.VerifyNativeLaunch(context.Background(), want); err == nil || !strings.Contains(err.Error(), "primary") {
		t.Fatalf("durable model mismatch error=%v", err)
	}
	if out, err := exec.Command("sqlite3", database, `UPDATE agent_invocations SET model='gpt-5.6-sol',model_provider='openrouter'`).CombinedOutput(); err != nil {
		t.Fatalf("provider fixture: %s %v", out, err)
	}
	if _, err := reader.VerifyNativeLaunch(context.Background(), want); err == nil || !strings.Contains(err.Error(), "primary") {
		t.Fatalf("durable provider mismatch error=%v", err)
	}
	if out, err := exec.Command("sqlite3", database, `UPDATE agent_invocations SET model_provider='openai'; UPDATE step_rounds SET global_config_yaml='changed'`).CombinedOutput(); err != nil {
		t.Fatalf("config fixture: %s %v", out, err)
	}
	if _, err := reader.VerifyNativeLaunch(context.Background(), want); err == nil || !strings.Contains(err.Error(), "global config") {
		t.Fatalf("durable global config mismatch error=%v", err)
	}
}

func TestVerifyNativeLaunchDefersAgentProvenanceUntilFirstInvocation(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	root := t.TempDir()
	project := t.TempDir()
	nativeWorktree := filepath.Join(root, "worktrees", "run")
	sql := `CREATE TABLE repos(id TEXT,working_path TEXT,default_branch TEXT);
CREATE TABLE runs(id TEXT,repo_id TEXT,branch TEXT,submitted_head_sha TEXT,launch_nonce TEXT,launch_validation_generation TEXT,no_mistakes_version TEXT,no_mistakes_build_sha TEXT,worktree_dir TEXT);
CREATE TABLE step_results(id TEXT,run_id TEXT,step_name TEXT);
CREATE TABLE step_rounds(id TEXT,step_result_id TEXT,round INTEGER,trusted_config_sha TEXT,global_config_yaml BLOB,created_at INTEGER);
CREATE TABLE agent_invocations(id TEXT,run_id TEXT,step_name TEXT,agent TEXT,model TEXT,model_provider TEXT,started_at INTEGER);
INSERT INTO repos VALUES('repo',` + sqlString(filepath.ToSlash(project)) + `,'main');
INSERT INTO runs VALUES('run','repo','feature','submitted','nonce','generation','v1.75.1','37ed232',` + sqlString(filepath.ToSlash(nativeWorktree)) + `);`
	database := filepath.Join(root, "state.sqlite")
	if out, err := exec.Command("sqlite3", database, sql).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %s %v", out, err)
	}
	want := NativeLaunchExpectation{
		RunID: "run", Project: project, RepoID: "repo", Branch: "feature", SubmittedHeadSHA: "submitted",
		LaunchNonce: "nonce", ValidationGeneration: "generation", TrustedSHA: "trusted", Primary: "codex",
		PrimaryModel: "gpt-5.6-sol", GlobalConfigSHA256: strings.Repeat("a", 64),
	}
	state, err := (Reader{Root: root, Commands: execx.OSRunner{}}).VerifyNativeLaunch(context.Background(), want)
	if err != nil || state.InvocationCount != 0 || !samePath(state.Worktree, nativeWorktree) {
		t.Fatalf("pre-agent launch state=%+v err=%v", state, err)
	}
	if out, err := exec.Command("sqlite3", database, `INSERT INTO agent_invocations VALUES('rebase','run','rebase','codex','gpt-5.6-sol','openai',1)`).CombinedOutput(); err != nil {
		t.Fatalf("rebase invocation fixture: %s %v", out, err)
	}
	state, err = (Reader{Root: root, Commands: execx.OSRunner{}}).VerifyNativeLaunch(context.Background(), want)
	if err != nil || state.InvocationCount != 1 || state.ReviewProvenance {
		t.Fatalf("pre-review rebase launch state=%+v err=%v", state, err)
	}
	if out, err := exec.Command("sqlite3", database, `UPDATE agent_invocations SET model_provider='openrouter'`).CombinedOutput(); err != nil {
		t.Fatalf("rebase provider fixture: %s %v", out, err)
	}
	if _, err := (Reader{Root: root, Commands: execx.OSRunner{}}).VerifyNativeLaunch(context.Background(), want); err == nil || !strings.Contains(err.Error(), "primary") {
		t.Fatalf("pre-review rebase provider error=%v", err)
	}
}

func TestNativeRunAtWorktreeDoesNotTreatAnEmptyRecordedPathAsTheCurrentDirectory(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	root := t.TempDir()
	project := t.TempDir()
	current, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	sql := `CREATE TABLE repos(id TEXT,working_path TEXT,default_branch TEXT);
CREATE TABLE runs(id TEXT,repo_id TEXT,branch TEXT,head_sha TEXT,submitted_head_sha TEXT,status TEXT,created_at INTEGER,launch_nonce TEXT,launch_validation_generation TEXT,worktree_dir TEXT,no_mistakes_version TEXT,no_mistakes_build_sha TEXT);
CREATE TABLE agent_invocations(run_id TEXT);
INSERT INTO repos VALUES('repo',` + sqlString(filepath.ToSlash(project)) + `,'main');
INSERT INTO runs VALUES('run','repo','feature','head','head','running',1,'nonce','generation','',NULL,NULL);`
	if out, err := exec.Command("sqlite3", filepath.Join(root, "state.sqlite"), sql).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %s %v", out, err)
	}
	reader := Reader{Root: root, Commands: execx.OSRunner{}}
	if _, err := reader.NativeRunAtWorktree(context.Background(), current); !errors.Is(err, ErrNoNativeRunAtWorktree) {
		t.Fatalf("empty worktree path matched current directory: %v", err)
	}
}

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

type originHeadRunner struct {
	output string
}

func (r originHeadRunner) Run(context.Context, execx.Request) (execx.Result, error) {
	return execx.Result{Stdout: []byte(r.output)}, nil
}

func TestOriginDefaultHeadIgnoresOtherAdvertisedRefs(t *testing.T) {
	const head = "0057c023d4440def35ea209cecf849335ee8c9df"
	reader := Reader{Commands: originHeadRunner{output: "ref: refs/heads/main\tHEAD\n" + head + "\tHEAD\nb7d55dd29a0d315a338091da0b14a3597a80a36d\trefs/heads/HEAD\n"}}
	got, err := reader.originDefaultHead(context.Background(), t.TempDir(), "main")
	if err != nil || got != head {
		t.Fatalf("originDefaultHead=%q, %v", got, err)
	}
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
	case "symbolic-ref --quiet --short HEAD":
		return execx.Result{Stdout: []byte("feature\n")}, nil
	case "status --porcelain --untracked-files=all":
		return execx.Result{}, nil
	case "rev-parse --verify HEAD^{commit}":
		return execx.Result{Stdout: []byte("89abcdef0123456789abcdef0123456789abcdef\n")}, nil
	case "show HEAD:.no-mistakes.yaml":
		if len(r.task) != 0 {
			return execx.Result{Stdout: r.task}, nil
		}
		return execx.Result{Stdout: []byte("agent: claude\nauto_fix: {review: 0, test: 1, lint: 1, rebase: 1, ci: 1}\n")}, nil
	case "ls-remote --symref origin HEAD":
		return execx.Result{Stdout: []byte("ref: refs/heads/main\tHEAD\n" + primaryRoutingSHA + "\tHEAD\n")}, nil
	case "rev-parse --verify refs/remotes/origin/main":
		return execx.Result{Stdout: []byte(primaryRoutingSHA + "\n")}, nil
	case "show " + primaryRoutingSHA + ":.no-mistakes.yaml":
		return execx.Result{Stdout: r.trusted}, nil
	default:
		return execx.Result{}, errors.New("unexpected git command")
	}
}

func TestCheckStartEvidenceBindsExactHeadTrustedSHAAndEffectivePrimary(t *testing.T) {
	sqlite, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	project := filepath.Join(t.TempDir(), "project")
	dir := t.TempDir()
	sql := `CREATE TABLE repos(id TEXT,working_path TEXT,default_branch TEXT);
CREATE TABLE runs(id TEXT,repo_id TEXT,branch TEXT,created_at INTEGER,status TEXT);
INSERT INTO repos VALUES('repo-bound',` + sqlString(filepath.ToSlash(project)) + `,'main');`
	if out, err := exec.Command(sqlite, filepath.Join(dir, "state.sqlite"), sql).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %s %v", out, err)
	}
	task := []byte("agent: codex\nauto_fix: {review: 0, test: 1, lint: 1, rebase: 1, ci: 1}\n")
	trusted := []byte("allow_repo_commands: true\n")
	reader := Reader{Root: dir, Commands: &primaryRoutingRunner{task: task, trusted: trusted}}
	evidence, err := reader.CheckStartEvidence(context.Background(), project, filepath.Join(project, "worktree"), "feature", testPolicy(t))
	if err != nil {
		t.Fatal(err)
	}
	if evidence.RepoID != "repo-bound" || evidence.Branch != "feature" || evidence.HeadSHA != "89abcdef0123456789abcdef0123456789abcdef" || evidence.DefaultBranch != "main" || evidence.TrustedSHA != primaryRoutingSHA || evidence.EffectivePrimary != "codex" {
		t.Fatalf("evidence=%+v", evidence)
	}
	if evidence.TaskConfigSHA256 == "" || evidence.TrustedConfigSHA256 == "" || evidence.TaskConfigSHA256 == evidence.TrustedConfigSHA256 {
		t.Fatalf("configuration digests=%+v", evidence)
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
	runPipelineGit(t, project, "switch", "-c", "feature")
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
