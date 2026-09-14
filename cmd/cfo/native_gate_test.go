package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
)

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

func TestFindPipelineLaunchContractBindsTaskPathAndLaunchIdentity(t *testing.T) {
	stateDir := t.TempDir()
	project := t.TempDir()
	contract := testPipelineLaunchContract(project)
	path := filepath.Join(stateDir, "tasktmp", contract.TaskID, pipelineLaunchContractName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := savePipelineLaunchContract(path, contract); err != nil {
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
CREATE TABLE runs(id TEXT,repo_id TEXT,branch TEXT,head_sha TEXT,submitted_head_sha TEXT,status TEXT,created_at INTEGER,launch_nonce TEXT,launch_validation_generation TEXT,worktree_dir TEXT);
INSERT INTO repos VALUES('repo',` + pipelineSQLString(filepath.ToSlash(project)) + `,'main');
INSERT INTO runs VALUES('run','repo','feature','head','head','running',1,NULL,NULL,` + pipelineSQLString(filepath.ToSlash(worktree)) + `);`
	if out, err := exec.Command("sqlite3", filepath.Join(nativeRoot, "state.sqlite"), sql).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %s %v", out, err)
	}
	reader := pipeline.Reader{Root: nativeRoot, Commands: execx.OSRunner{}}
	if err := authorizeNativeGateAgent(context.Background(), home.Home{State: stateDir}, reader, worktree); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("sqlite3", filepath.Join(nativeRoot, "state.sqlite"), `UPDATE runs SET launch_nonce='aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',launch_validation_generation='native-generation'`).CombinedOutput(); err != nil {
		t.Fatalf("update fixture: %s %v", out, err)
	}
	if err := authorizeNativeGateAgent(context.Background(), home.Home{State: stateDir}, reader, worktree); err != nil {
		t.Fatalf("strict native run was not preserved: %v", err)
	}
	if out, err := exec.Command("sqlite3", filepath.Join(nativeRoot, "state.sqlite"), `UPDATE runs SET launch_validation_generation='cfo-v1-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb'`).CombinedOutput(); err != nil {
		t.Fatalf("update managed fixture: %s %v", out, err)
	}
	if err := authorizeNativeGateAgent(context.Background(), home.Home{State: stateDir}, reader, worktree); err == nil || !strings.Contains(err.Error(), "no matching CFO launch contract") {
		t.Fatalf("managed run without contract error=%v", err)
	}
	if out, err := exec.Command("sqlite3", filepath.Join(nativeRoot, "state.sqlite"), `DELETE FROM runs`).CombinedOutput(); err != nil {
		t.Fatalf("delete fixture: %s %v", out, err)
	}
	if err := authorizeNativeGateAgent(context.Background(), home.Home{State: stateDir}, reader, worktree); err != nil {
		t.Fatalf("native operation outside a run was not preserved: %v", err)
	}
}

func testPipelineLaunchContract(project string) pipelineLaunchContract {
	return pipelineLaunchContract{
		Version: 1, TaskID: "native-gate-test", PolicyHash: strings.Repeat("c", 64), Project: project,
		Checked: pipeline.StartEvidence{
			RepoID: "repo", Branch: "feature", HeadSHA: strings.Repeat("1", 40), DefaultBranch: "main",
			TrustedSHA: strings.Repeat("2", 40), TaskConfigSHA256: strings.Repeat("3", 64),
			TrustedConfigSHA256: strings.Repeat("4", 64), EffectivePrimary: "codex",
		},
		ConfigSHA256: strings.Repeat("5", 64), LaunchNonce: strings.Repeat("a", 32),
		ValidationGeneration: cfoValidationGenerationPrefix + strings.Repeat("b", 32),
	}
}

func pipelineSQLString(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
