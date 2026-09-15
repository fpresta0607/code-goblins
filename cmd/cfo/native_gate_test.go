package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
)

type nativeGateStatusRunner struct {
	result  execx.Result
	err     error
	request execx.Request
}

func (r *nativeGateStatusRunner) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	r.request = request
	return r.result, r.err
}

func TestStripNativeGateMarkerRecognizesOnlyNativeCodexPositions(t *testing.T) {
	for _, test := range []struct {
		name   string
		args   []string
		want   string
		marked bool
	}{
		{name: "exec", args: []string{"exec", nativeGateMarker, "-c", "model=x"}, want: "exec -c model=x", marked: true},
		{name: "resume", args: []string{"exec", "resume", nativeGateMarker, "session", "-"}, want: "exec resume session -", marked: true},
		{name: "ordinary argument", args: []string{"pipeline", "run", "task", "--intent", nativeGateMarker}, want: "pipeline run task --intent --cfo-native-gate"},
		{name: "wrong exec position", args: []string{"exec", "-c", nativeGateMarker}, want: "exec -c --cfo-native-gate"},
	} {
		t.Run(test.name, func(t *testing.T) {
			args, marked := stripNativeGateMarker(test.args)
			if marked != test.marked || strings.Join(args, " ") != test.want {
				t.Fatalf("args=%q marked=%t", args, marked)
			}
		})
	}
}

func TestSubscriptionOnlyNativeGateEnvironmentStripsBillingKeysCaseInsensitively(t *testing.T) {
	got := subscriptionOnlyNativeGateEnvironment([]string{
		"PATH=C:\\tools",
		"openai_api_key=secret",
		"OPENAI_BASE_URL=https://openrouter.ai/api/v1",
		"OPENAI_KEY=secret",
		"CoDeX_ApI_KeY=secret",
		"OpenRouter_Api_Key=secret",
		"CFO_HOME=C:\\fleet",
	})
	want := []string{"PATH=C:\\tools", "CFO_HOME=C:\\fleet"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("environment=%q, want %q", got, want)
	}
}

func TestSubscriptionOnlyNativeGateArgumentsForceOpenAIProvider(t *testing.T) {
	got := subscriptionOnlyNativeGateArguments([]string{"exec", "-c", `model_provider="openrouter"`, "-c", `openai_base_url="https://openrouter.ai/api/v1"`, "-"})
	want := []string{"exec", "-c", `model_provider="openrouter"`, "-c", `openai_base_url="https://openrouter.ai/api/v1"`, "-", "-c", `model_provider="openai"`, "-c", `forced_login_method="chatgpt"`, "-c", `openai_base_url="https://chatgpt.com/backend-api/codex"`, "-c", `chatgpt_base_url="https://chatgpt.com/backend-api/"`}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("arguments=%q, want %q", got, want)
	}
}

func TestNativeGateDelegationPreservesUnmanagedProviderAndCredentials(t *testing.T) {
	args := []string{"exec", "-c", `model_provider="openrouter"`, "-"}
	env := []string{"PATH=C:\\tools", "OPENROUTER_API_KEY=secret"}
	gotArgs, gotEnv := nativeGateDelegation(args, env, false)
	if strings.Join(gotArgs, "\n") != strings.Join(args, "\n") || strings.Join(gotEnv, "\n") != strings.Join(env, "\n") {
		t.Fatalf("unmanaged delegation args=%q env=%q", gotArgs, gotEnv)
	}
}

func TestRequireManagedNativeGateChatGPTRejectsPersistedAPIKey(t *testing.T) {
	for _, test := range []struct {
		name    string
		status  string
		wantErr bool
	}{
		{name: "ChatGPT", status: "Logged in using ChatGPT"},
		{name: "API key", status: "Logged in using an API key - sk-...", wantErr: true},
		{name: "logged out", status: "Not logged in", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &nativeGateStatusRunner{result: execx.Result{Stderr: []byte(test.status + "\n")}}
			err := requireManagedNativeGateChatGPT(context.Background(), runner, []string{"PATH=C:\\tools"})
			if (err != nil) != test.wantErr {
				t.Fatalf("status=%q error=%v", test.status, err)
			}
			if strings.Join(runner.request.Args, " ") != `login status -c model_provider="openai" -c forced_login_method="chatgpt" -c openai_base_url="https://chatgpt.com/backend-api/codex" -c chatgpt_base_url="https://chatgpt.com/backend-api/"` {
				t.Fatalf("status arguments=%q", runner.request.Args)
			}
		})
	}
}

func TestFindPipelineLaunchContractBindsTaskPathAndLaunchIdentity(t *testing.T) {
	stateDir := t.TempDir()
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "sqlite3.exe"), []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	contract := testPipelineLaunchContract(t, project)
	contract.Status = pipelineLaunchContractPending
	path := filepath.Join(stateDir, "tasktmp", contract.TaskID, pipelineLaunchContractName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := saveTestPipelineLaunchEvidence(path, contract); err != nil {
		t.Fatal(err)
	}
	got, err := findPipelineLaunchContract(stateDir, contract.LaunchNonce, contract.ValidationGeneration)
	if err != nil || got != contract {
		t.Fatalf("contract=%+v err=%v", got, err)
	}
	if _, err := findPipelineLaunchContract(stateDir, strings.Repeat("b", 32), contract.ValidationGeneration); err == nil {
		t.Fatal("unbound launch identity accepted")
	}
}

func TestFindPipelineLaunchContractIgnoresUnrelatedLegacyContract(t *testing.T) {
	stateDir := t.TempDir()
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "sqlite3.exe"), []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	legacy := testPipelineLaunchContract(t, project)
	legacy.TaskID = "a-legacy-task"
	legacy.Status = ""
	legacy.LaunchNonce = strings.Repeat("d", 32)
	legacy.ValidationGeneration = strings.Repeat("e", 32)
	legacyData, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(stateDir, "tasktmp", legacy.TaskID, pipelineLaunchContractName)
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, legacyData, 0o600); err != nil {
		t.Fatal(err)
	}

	current := testPipelineLaunchContract(t, project)
	current.TaskID = "current-task"
	currentPath := filepath.Join(stateDir, "tasktmp", current.TaskID, pipelineLaunchContractName)
	if err := os.MkdirAll(filepath.Dir(currentPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := saveTestPipelineLaunchEvidence(currentPath, current); err != nil {
		t.Fatal(err)
	}

	got, err := findPipelineLaunchContract(stateDir, current.LaunchNonce, current.ValidationGeneration)
	if err != nil || got != current {
		t.Fatalf("contract=%+v err=%v", got, err)
	}
}

func TestAuthorizeNativeGateAgentPreservesUnmanagedNativeRuns(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	stateDir := t.TempDir()
	nativeRoot := t.TempDir()
	project := t.TempDir()
	worktree := t.TempDir()
	if err := os.MkdirAll(filepath.Join(stateDir, "tasktmp"), 0o755); err != nil {
		t.Fatal(err)
	}
	sql := `CREATE TABLE repos(id TEXT,working_path TEXT,default_branch TEXT);
CREATE TABLE runs(id TEXT,repo_id TEXT,branch TEXT,head_sha TEXT,submitted_head_sha TEXT,status TEXT,created_at INTEGER,launch_nonce TEXT,launch_validation_generation TEXT,worktree_dir TEXT,no_mistakes_version TEXT,no_mistakes_build_sha TEXT);
CREATE TABLE agent_invocations(run_id TEXT);
INSERT INTO repos VALUES('repo',` + pipelineSQLString(filepath.ToSlash(project)) + `,'main');
INSERT INTO runs VALUES('run','repo','feature','head','head','running',1,NULL,NULL,` + pipelineSQLString(filepath.ToSlash(worktree)) + `,NULL,NULL);`
	if out, err := exec.Command("sqlite3", filepath.Join(nativeRoot, "state.sqlite"), sql).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %s %v", out, err)
	}
	reader := pipeline.Reader{Root: nativeRoot, Commands: execx.OSRunner{}}
	if managed, err := authorizeNativeGateAgent(context.Background(), home.Home{State: stateDir}, reader, worktree); err != nil || managed {
		t.Fatal(err)
	}
	if out, err := exec.Command("sqlite3", filepath.Join(nativeRoot, "state.sqlite"), `UPDATE runs SET launch_nonce='aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',launch_validation_generation='native-generation'`).CombinedOutput(); err != nil {
		t.Fatalf("update fixture: %s %v", out, err)
	}
	if managed, err := authorizeNativeGateAgent(context.Background(), home.Home{State: stateDir}, reader, worktree); err != nil || managed {
		t.Fatalf("strict native run was not preserved: %v", err)
	}
	if out, err := exec.Command("sqlite3", filepath.Join(nativeRoot, "state.sqlite"), `UPDATE runs SET status=NULL,launch_validation_generation='cfo-v1-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb'`).CombinedOutput(); err != nil {
		t.Fatalf("update managed fixture: %s %v", out, err)
	}
	if _, err := authorizeNativeGateAgent(context.Background(), home.Home{State: stateDir}, reader, worktree); err == nil || !strings.Contains(err.Error(), "no matching CFO launch contract") {
		t.Fatalf("managed run without contract error=%v", err)
	}
	if out, err := exec.Command("sqlite3", filepath.Join(nativeRoot, "state.sqlite"), `DELETE FROM runs`).CombinedOutput(); err != nil {
		t.Fatalf("delete fixture: %s %v", out, err)
	}
	if managed, err := authorizeNativeGateAgent(context.Background(), home.Home{State: stateDir}, reader, worktree); err != nil || managed {
		t.Fatalf("native operation outside a run was not preserved: %v", err)
	}
}

func TestAuthorizeNativeGateAgentRefusesPendingContractAfterFirstInvocation(t *testing.T) {
	sqlitePath, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	sqlitePath, err = filepath.Abs(sqlitePath)
	if err != nil {
		t.Fatal(err)
	}
	stateDir := t.TempDir()
	nativeRoot := t.TempDir()
	project := t.TempDir()
	worktree := t.TempDir()
	contract := testPipelineLaunchContract(t, project)
	contract.Status = pipelineLaunchContractPending
	contract.Checked.RepoID = "repo"
	contract.Checked.Branch = "feature"
	contract.Checked.HeadSHA = "head"
	contract.LaunchNonce = strings.Repeat("a", 32)
	contract.ValidationGeneration = cfoValidationGenerationPrefix + strings.Repeat("b", 32)
	contract.SQLitePath = sqlitePath
	contractPath := filepath.Join(stateDir, "tasktmp", contract.TaskID, pipelineLaunchContractName)
	if err := os.MkdirAll(filepath.Dir(contractPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := saveTestPipelineLaunchEvidence(contractPath, contract); err != nil {
		t.Fatal(err)
	}
	sql := `CREATE TABLE repos(id TEXT,working_path TEXT,default_branch TEXT);
CREATE TABLE runs(id TEXT,repo_id TEXT,branch TEXT,head_sha TEXT,submitted_head_sha TEXT,status TEXT,created_at INTEGER,launch_nonce TEXT,launch_validation_generation TEXT,worktree_dir TEXT,no_mistakes_version TEXT,no_mistakes_build_sha TEXT);
CREATE TABLE agent_invocations(run_id TEXT);
INSERT INTO repos VALUES('repo',` + pipelineSQLString(filepath.ToSlash(project)) + `,'main');
INSERT INTO runs VALUES('run','repo','feature','head','head','running',1,'` + contract.LaunchNonce + `','` + contract.ValidationGeneration + `',` + pipelineSQLString(filepath.ToSlash(worktree)) + `,'v1.75.1','37ed232');
INSERT INTO agent_invocations VALUES('run');`
	if out, err := exec.Command(sqlitePath, filepath.Join(nativeRoot, "state.sqlite"), sql).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %s %v", out, err)
	}
	reader := pipeline.Reader{Root: nativeRoot, Commands: execx.OSRunner{}, SQLitePath: sqlitePath}
	_, err = authorizeNativeGateAgent(context.Background(), home.Home{State: stateDir}, reader, worktree)
	if err == nil || !strings.Contains(err.Error(), "pending") {
		t.Fatalf("pending contract after invocation error=%v", err)
	}
	contract, err = bindPipelineLaunchContract(contractPath, contract, "run", pipelineLaunchContractAuthorized)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := authorizeNativeGateAgent(context.Background(), home.Home{State: stateDir}, reader, worktree); err == nil || strings.Contains(err.Error(), "pending") {
		t.Fatalf("authorized automatic followup lifecycle error=%v", err)
	}
	contract.CFOExecutablePath = sqlitePath
	contract.CFOExecutableSHA256, err = fileSHA256(sqlitePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := savePipelineLaunchContract(contractPath, contract); err != nil {
		t.Fatal(err)
	}
	if _, err := authorizeNativeGateAgent(context.Background(), home.Home{State: stateDir}, reader, worktree); err == nil || !strings.Contains(err.Error(), "CFO executable identity") {
		t.Fatalf("different CFO executable error=%v", err)
	}
}

func TestBindPipelineLaunchContractAuthorizesAutomaticFollowups(t *testing.T) {
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "sqlite3.exe"), []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	contract := testPipelineLaunchContract(t, project)
	path := filepath.Join(t.TempDir(), contract.TaskID, pipelineLaunchContractName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := saveTestPipelineLaunchEvidence(path, contract); err != nil {
		t.Fatal(err)
	}
	authorized, err := bindPipelineLaunchContract(path, contract, "run-bound", pipelineLaunchContractAuthorized)
	if err != nil {
		t.Fatal(err)
	}
	if authorized.Status != pipelineLaunchContractAuthorized || authorized.RunID != "run-bound" {
		t.Fatalf("authorized contract=%+v", authorized)
	}
	claim, err := loadPipelineLaunchClaim(filepath.Join(filepath.Dir(path), pipelineLaunchClaimName))
	if err != nil || claim.RunID != "run-bound" {
		t.Fatalf("bound claim=%+v err=%v", claim, err)
	}
	loaded, err := loadPipelineLaunchContract(path)
	if err != nil || loaded != authorized {
		t.Fatalf("loaded contract=%+v err=%v", loaded, err)
	}
}

func TestNativeGateReaderIgnoresStaleGlobalRuntimeWithoutScopedContract(t *testing.T) {
	h := home.Home{State: t.TempDir()}
	if err := os.WriteFile(filepath.Join(h.State, "native-gate-runtime.json"), []byte("stale and invalid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	reader, inspect, err := nativeGateReader(h, t.TempDir(), t.TempDir())
	if err != nil || inspect || reader.Commands != nil {
		t.Fatalf("reader=%+v inspect=%t err=%v", reader, inspect, err)
	}
}

func TestNativeGateReaderUsesLaunchContractSQLiteOutsidePath(t *testing.T) {
	sqlitePath, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	sqlitePath, err = filepath.Abs(sqlitePath)
	if err != nil {
		t.Fatal(err)
	}
	h := home.Home{State: t.TempDir()}
	nativeRoot := t.TempDir()
	worktree := filepath.Join(t.TempDir(), "run-bound")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	gitDir := filepath.Join(nativeRoot, "repos", "repo.git", "worktrees", "run-bound")
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: "+gitDir+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "sqlite3.exe"), []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	contract := testPipelineLaunchContract(t, project)
	contract.Status = pipelineLaunchContractVerified
	contract.RunID = "run-bound"
	contract.SQLitePath = sqlitePath
	contractPath := filepath.Join(h.State, "tasktmp", contract.TaskID, pipelineLaunchContractName)
	if err := os.MkdirAll(filepath.Dir(contractPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := saveTestPipelineLaunchEvidence(contractPath, contract); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	reader, inspect, err := nativeGateReader(h, nativeRoot, worktree)
	if err != nil || !inspect || !filepath.IsAbs(reader.SQLitePath) || !strings.EqualFold(reader.SQLitePath, sqlitePath) {
		t.Fatalf("reader=%+v inspect=%t err=%v", reader, inspect, err)
	}
	unmanaged := filepath.Join(filepath.Dir(worktree), "unmanaged-run")
	if err := os.MkdirAll(unmanaged, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unmanaged, ".git"), []byte("gitdir: "+filepath.Join(nativeRoot, "repos", "repo.git", "worktrees", "unmanaged-run")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	reader, inspect, err = nativeGateReader(h, nativeRoot, unmanaged)
	if err != nil || inspect || reader.Commands != nil {
		t.Fatalf("unmanaged native invocation did not pass through: reader=%+v inspect=%t err=%v", reader, inspect, err)
	}
}

func TestNativeGateReaderFailsClosedWhenManagedContractIsMissing(t *testing.T) {
	h := home.Home{State: t.TempDir()}
	nativeRoot := t.TempDir()
	worktree := filepath.Join(t.TempDir(), "run-bound")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	gitDir := filepath.Join(nativeRoot, "repos", "repo.git", "worktrees", "run-bound")
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: "+gitDir+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	contract := testPipelineLaunchContract(t, t.TempDir())
	contract.RunID = "run-bound"
	claim := pipelineLaunchClaimForContract(contract)
	claim.RunID = contract.RunID
	claimPath := filepath.Join(h.State, "tasktmp", contract.TaskID, pipelineLaunchClaimName)
	if err := os.MkdirAll(filepath.Dir(claimPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := savePipelineLaunchClaim(claimPath, claim); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	if _, _, err := nativeGateReader(h, nativeRoot, worktree); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("missing managed contract error=%v", err)
	}
}

func TestNativeGateReaderFailsClosedWhenManagedClaimIsMissing(t *testing.T) {
	h := home.Home{State: t.TempDir()}
	nativeRoot := t.TempDir()
	worktree := filepath.Join(t.TempDir(), "run-bound")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	gitDir := filepath.Join(nativeRoot, "repos", "repo.git", "worktrees", "run-bound")
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: "+gitDir+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "sqlite3.exe"), []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	contract := testPipelineLaunchContract(t, project)
	contract.RunID = "run-bound"
	contract.Status = pipelineLaunchContractAuthorized
	contractPath := filepath.Join(h.State, "tasktmp", contract.TaskID, pipelineLaunchContractName)
	if err := os.MkdirAll(filepath.Dir(contractPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := savePipelineLaunchContract(contractPath, contract); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	if _, _, err := nativeGateReader(h, nativeRoot, worktree); err == nil || !strings.Contains(err.Error(), "claim is missing") {
		t.Fatalf("missing managed claim error=%v", err)
	}
}

func TestNativeGateReaderFailsClosedWhenAllManagedEvidenceIsMissingOrInvalid(t *testing.T) {
	sqlitePath, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	for _, test := range []struct {
		name     string
		evidence string
	}{
		{name: "missing"},
		{name: "invalid", evidence: "not json\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := home.Home{State: t.TempDir()}
			nativeRoot := t.TempDir()
			worktree := filepath.Join(t.TempDir(), "run-bound")
			if err := os.MkdirAll(worktree, 0o755); err != nil {
				t.Fatal(err)
			}
			gitDir := filepath.Join(nativeRoot, "repos", "repo.git", "worktrees", "run-bound")
			if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: "+gitDir+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if test.evidence != "" {
				dir := filepath.Join(h.State, "tasktmp", "task")
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				for _, name := range []string{pipelineLaunchContractName, pipelineLaunchClaimName} {
					if err := os.WriteFile(filepath.Join(dir, name), []byte(test.evidence), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			project := t.TempDir()
			sql := `CREATE TABLE repos(id TEXT,working_path TEXT,default_branch TEXT);
CREATE TABLE runs(id TEXT,repo_id TEXT,branch TEXT,head_sha TEXT,submitted_head_sha TEXT,status TEXT,created_at INTEGER,launch_nonce TEXT,launch_validation_generation TEXT,worktree_dir TEXT,no_mistakes_version TEXT,no_mistakes_build_sha TEXT);
CREATE TABLE agent_invocations(run_id TEXT);
INSERT INTO repos VALUES('repo',` + pipelineSQLString(filepath.ToSlash(project)) + `,'main');
INSERT INTO runs VALUES('run-bound','repo','feature','head','head','running',1,'` + strings.Repeat("a", 32) + `','` + cfoValidationGenerationPrefix + strings.Repeat("b", 32) + `',` + pipelineSQLString(filepath.ToSlash(worktree)) + `,'v1.75.1','37ed232');`
			if out, err := exec.Command(sqlitePath, filepath.Join(nativeRoot, "state.sqlite"), sql).CombinedOutput(); err != nil {
				t.Fatalf("fixture: %s %v", out, err)
			}
			if _, _, err := nativeGateReader(h, nativeRoot, worktree); err == nil || !strings.Contains(err.Error(), "evidence") {
				t.Fatalf("managed run with %s evidence error=%v", test.name, err)
			}
		})
	}
}

func testPipelineLaunchContract(t *testing.T, project string) pipelineLaunchContract {
	t.Helper()
	executable, digest, err := currentCFOExecutableEvidence()
	if err != nil {
		t.Fatal(err)
	}
	return pipelineLaunchContract{
		Version: 1, Status: pipelineLaunchContractPending, TaskID: "native-gate-test", PolicyHash: strings.Repeat("c", 64), Project: project,
		Checked: pipeline.StartEvidence{
			RepoID: "repo", Branch: "feature", HeadSHA: strings.Repeat("1", 40), DefaultBranch: "main",
			TrustedSHA: strings.Repeat("2", 40), TaskConfigSHA256: strings.Repeat("3", 64),
			TrustedConfigSHA256: strings.Repeat("4", 64), EffectivePrimary: "codex",
		},
		ConfigSHA256: strings.Repeat("5", 64), LaunchNonce: strings.Repeat("a", 32),
		ValidationGeneration: cfoValidationGenerationPrefix + strings.Repeat("b", 32),
		SQLitePath:           filepath.Join(project, "sqlite3.exe"),
		CFOExecutablePath:    executable,
		CFOExecutableSHA256:  digest,
	}
}

func saveTestPipelineLaunchEvidence(path string, contract pipelineLaunchContract) error {
	if err := savePipelineLaunchContract(path, contract); err != nil {
		return err
	}
	return savePipelineLaunchClaim(filepath.Join(filepath.Dir(path), pipelineLaunchClaimName), pipelineLaunchClaimForContract(contract))
}

func pipelineSQLString(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
