package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

func TestRecoverInheritedPublicationNativeJourney(t *testing.T) {
	source := os.Getenv("CFO_RECOVERY_NATIVE_SOURCE")
	if source == "" {
		t.Skip("requires a matching native source checkout to reuse its admission/restart fixtures")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	runner := execx.OSRunner{}
	head, err := runner.Run(ctx, execx.Request{Dir: source, Name: "git", Args: []string{"rev-parse", "HEAD"}})
	if err != nil || head.ExitCode != 0 {
		t.Fatalf("native source identity: %+v %v", head, err)
	}
	version, err := runner.Run(ctx, execx.Request{Name: "no-mistakes", Args: []string{"--version"}})
	if err != nil || version.ExitCode != 0 || !strings.Contains(string(version.Stdout), strings.TrimSpace(string(head.Stdout))) {
		t.Fatalf("native executable does not match source: %s %v", version.Stdout, err)
	}
	dirty, err := runner.Run(ctx, execx.Request{Dir: source, Name: "git", Args: []string{"status", "--porcelain"}})
	if err != nil || dirty.ExitCode != 0 || len(dirty.Stdout) != 0 {
		t.Fatal("native fixture source must be clean")
	}
	controller, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	fixture := os.Getenv("CFO_RECOVERY_NATIVE_FIXTURE")
	manifest, err := os.ReadFile(fixture + ".json")
	if err != nil {
		t.Fatal("requires the prepared native fixture executable and its build manifest")
	}
	var binding struct{ NativeCommit, FixtureSHA256, ExecutableSHA256, GoVersion string }
	if err := json.Unmarshal(manifest, &binding); err != nil {
		t.Fatal(err)
	}
	for path, expected := range map[string]string{
		fixture: binding.ExecutableSHA256,
		filepath.Join(controller, "internal", "pipeline", "testdata", "native-recovery-journey_test.go"): binding.FixtureSHA256,
	} {
		data, err := os.ReadFile(path)
		if err != nil || fmt.Sprintf("%x", sha256.Sum256(data)) != expected {
			t.Fatalf("prepared native fixture does not match %s: %v", path, err)
		}
	}
	toolchain, err := runner.Run(ctx, execx.Request{Name: "go", Args: []string{"version"}})
	if err != nil || toolchain.ExitCode != 0 || binding.GoVersion != strings.TrimSpace(string(toolchain.Stdout)) || binding.NativeCommit != strings.TrimSpace(string(head.Stdout)) {
		t.Fatal("prepared native fixture source or toolchain changed")
	}
	controllerTests, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	result, err := runner.Run(ctx, execx.Request{Dir: source, Env: append(os.Environ(), "CFO_RECOVERY_NATIVE_CONTROLLER="+controller, "CFO_RECOVERY_CONTROLLER_TESTS="+controllerTests), Name: fixture, Args: []string{"-test.run", "^TestController(InheritedPublicationJourney|AuthoredCancellationRemainsRefused)$", "-test.v", "-test.timeout", "3m"}, KillTree: true})
	t.Logf("native=%s\n%s%s", version.Stdout, result.Stdout, result.Stderr)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("managed native journey: exit=%d error=%v", result.ExitCode, err)
	}
}

// The native admission/restart fixture invokes this seam while its isolated
// Git and SQLite state is alive. No native boundary is replaced by a runner.
func TestRecoverInheritedPublicationNativeFixture(t *testing.T) {
	root := os.Getenv("CFO_RECOVERY_NATIVE_ROOT")
	if root == "" {
		t.Skip("requires the native admission/restart fixture and matching native executable")
	}
	marker, err := os.ReadFile(filepath.Join(root, ".controller-recovery-fixture"))
	if err != nil || string(marker) != "isolated native admission fixture\n" {
		t.Fatal("requires an explicitly prepared isolated native fixture")
	}
	project := os.Getenv("CFO_RECOVERY_NATIVE_PROJECT")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	reader := Reader{Root: root, Commands: fixtureCommands{execx.OSRunner{}}}
	env := append(os.Environ(), "NM_HOME="+root)
	run, err := reader.latestRecoveryCandidate(ctx, project, "feature")
	if err != nil {
		t.Fatal(err)
	}
	readDatabase := func() string {
		t.Helper()
		result, err := reader.Commands.Run(ctx, execx.Request{Name: "sqlite3", Args: []string{"-readonly", filepath.Join(root, "state.sqlite"), ".dump"}})
		if err != nil || result.ExitCode != 0 {
			t.Fatalf("read fixture: %s %v", result.Stderr, err)
		}
		return string(result.Stdout)
	}
	writeDatabase := func(sql string) {
		t.Helper()
		result, err := reader.Commands.Run(ctx, execx.Request{Name: "sqlite3", Args: []string{filepath.Join(root, "state.sqlite"), sql}})
		if err != nil || result.ExitCode != 0 {
			t.Fatalf("change fixture: %s %v", result.Stderr, err)
		}
	}
	original := readDatabase()
	if os.Getenv("CFO_RECOVERY_EXPECT_USER_OWNED") == "1" {
		status, _, err := reader.readRecoverySync(ctx, project, env)
		if err != nil || !status.userOwned(run, true) {
			t.Fatalf("cancellation did not already release source: %+v %v", status, err)
		}
		if _, err := reader.RecoverKeepLocal(ctx, project, project, "feature", env); err == nil {
			t.Fatal("separate user-owned cancellation was admitted through inherited-publication recovery")
		}
		if readDatabase() != original {
			t.Fatal("refused cancellation changed history")
		}
		return
	}
	for _, scenario := range []struct{ name, table, column, value, where string }{
		{"active run", "runs", "status", "'running'", "id=" + sqlString(run.RunID)},
		{"ordinary published run", "runs", "head_sha", "last_pushed_sha", "id=" + sqlString(run.RunID)},
		{"child published source", "runs", "last_pushed_sha", "head_sha", "id=" + sqlString(run.RunID)},
		{"stale push generation", "runs", "push_generation", "push_generation+1", "id=" + sqlString(run.RunID)},
		{"changed PR identity", "runs", "pr_url", "'https://github.com/test/repo/pull/99'", "id=" + sqlString(run.RunID)},
		{"changed launch generation", "runs", "launch_validation_generation", "'stale'", "id=" + sqlString(run.RunID)},
		{"changed launch nonce", "runs", "launch_nonce", "'stale'", "id=" + sqlString(run.RunID)},
		{"changed original intent", "runs", "intent", "'replacement intent'", "id=" + sqlString(run.RunID)},
		{"changed source", "runs", "head_sha", "last_pushed_sha", "id=" + sqlString(run.RunID)},
		{"unverified source", "runs", "terminal_head_verified_at", "NULL", "id=" + sqlString(run.RunID)},
		{"uncertain source", "runs", "terminal_source_state", "'unknown'", "id=" + sqlString(run.RunID)},
		{"active publisher", "runs", "push_active", "1", "id=" + sqlString(run.RunID)},
		{"active agent", "step_results", "agent_pid", "1", "run_id=" + sqlString(run.RunID)},
		{"renewed repair budget", "step_results", "auto_fix_limit", "1", "run_id=" + sqlString(run.RunID)},
		{"changed predecessor history", "runs", "error", "'changed history'", "id=(SELECT predecessor_id FROM validation_admissions WHERE child_id=" + sqlString(run.RunID) + ")"},
	} {
		t.Run("refuses "+scenario.name, func(t *testing.T) {
			var saved []struct {
				Value string `json:"value"`
			}
			if err := reader.query(ctx, "SELECT quote("+scenario.column+") AS value FROM "+scenario.table+" WHERE "+scenario.where, &saved); err != nil || len(saved) != 1 {
				t.Fatalf("read original fixture column: %v", err)
			}
			writeDatabase("UPDATE " + scenario.table + " SET " + scenario.column + "=" + scenario.value + " WHERE " + scenario.where)
			defer writeDatabase("UPDATE " + scenario.table + " SET " + scenario.column + "=" + saved[0].Value + " WHERE " + scenario.where)
			changed := readDatabase()
			if _, err := reader.RecoverKeepLocal(ctx, project, project, "feature", env); err == nil {
				t.Fatal("unsafe recovery accepted")
			}
			if readDatabase() != changed {
				t.Fatal("refusal changed native history or custody")
			}
		})
		if readDatabase() != original {
			t.Fatal("negative fixture was not restored exactly")
		}
	}
	for _, scenario := range []struct{ name, identity, timestamp string }{{"nonlatest run", "ZZZZZZZZZZZZZZZZZZZZZZZZZZ", "(SELECT MAX(created_at)+1 FROM runs)"}, {"another active owner", "00000000000000000000000000", "0"}} {
		t.Run("refuses "+scenario.name, func(t *testing.T) {
			writeDatabase("INSERT INTO runs(id,repo_id,branch,head_sha,base_sha,status,created_at,updated_at) VALUES(" + sqlString(scenario.identity) + "," + sqlString(run.RepoID) + ",'feature'," + sqlString(run.RecordedHead) + "," + sqlString(run.PushedHead) + ",'running'," + scenario.timestamp + ",0)")
			defer writeDatabase("DELETE FROM runs WHERE id=" + sqlString(scenario.identity))
			changed := readDatabase()
			if _, err := reader.RecoverKeepLocal(ctx, project, project, "feature", env); err == nil {
				t.Fatal("competing ownership accepted")
			}
			if readDatabase() != changed {
				t.Fatal("refusal changed competing ownership")
			}
		})
	}
	for _, mutation := range []string{
		"DELETE FROM validation_admissions",
		"UPDATE validation_admissions SET event_json='unreadable'",
		"UPDATE validation_admissions SET id='another-admission'",
		"UPDATE validation_admissions SET event_json=json_set(event_json,'$.InitialChild.PushGeneration',99)",
		"INSERT INTO step_rounds(id,step_result_id,round,trigger_type,duration_ms,created_at) SELECT 'unauthorized-repair',s.id,1,'user_fix',0,0 FROM step_results s WHERE s.run_id=" + sqlString(run.RunID),
	} {
		t.Run("refuses damaged provenance "+mutation, func(t *testing.T) {
			shadow := Reader{Root: t.TempDir(), Commands: reader.Commands}
			path := filepath.Join(shadow.Root, "state.sqlite")
			result, err := reader.Commands.Run(ctx, execx.Request{Name: "sqlite3", Args: []string{"-readonly", filepath.Join(root, "state.sqlite"), ".backup '" + filepath.ToSlash(path) + "'"}})
			if err != nil || result.ExitCode != 0 {
				t.Fatalf("copy native fixture ledger: %s %v", result.Stderr, err)
			}
			if _, err := shadow.inheritedRecovery(ctx, run); err != nil {
				t.Fatalf("unaltered provenance must be readable: %v", err)
			}
			// Corruption is injected only into this separate database copy.
			writeFixtureSQLite(t, path, "DROP TRIGGER validation_admission_immutable;"+mutation)
			if _, err := shadow.inheritedRecovery(ctx, run); err == nil {
				t.Fatal("damaged admission provenance accepted")
			}
		})
	}
	bare := filepath.Join(root, "repos", run.RepoID+".git")
	t.Run("refuses moved gate branch", func(t *testing.T) {
		gate := runPipelineGit(t, bare, "rev-parse", "refs/heads/feature")
		runPipelineGit(t, bare, "update-ref", "refs/heads/feature", run.RecordedHead)
		defer runPipelineGit(t, bare, "update-ref", "refs/heads/feature", gate)
		if _, err := reader.RecoverKeepLocal(ctx, project, project, "feature", env); err == nil {
			t.Fatal("moved gate branch accepted")
		}
	})
	for _, ref := range []string{"refs/no-mistakes/recover/" + run.RunID, "refs/no-mistakes/authored-candidate/" + os.Getenv("CFO_RECOVERY_NATIVE_ADMISSION")} {
		t.Run("refuses missing "+ref, func(t *testing.T) {
			head := runPipelineGit(t, bare, "rev-parse", ref)
			runPipelineGit(t, bare, "update-ref", "-d", ref)
			defer runPipelineGit(t, bare, "update-ref", ref, head)
			if _, err := reader.RecoverKeepLocal(ctx, project, project, "feature", env); err == nil {
				t.Fatal("missing source anchor accepted")
			}
		})
	}
	for _, directory := range []string{project, filepath.Join(root, "worktrees", run.RepoID, run.RunID)} {
		t.Run("refuses dirty "+filepath.Base(directory), func(t *testing.T) {
			path := filepath.Join(directory, "unfinished.txt")
			writeFixtureFile(t, path, "unfinished source\n")
			defer os.Remove(path)
			if _, err := reader.RecoverKeepLocal(ctx, project, project, "feature", env); err == nil {
				t.Fatal("dirty source accepted")
			}
		})
	}
	if original != readDatabase() {
		t.Fatal("refusal matrix changed the original history")
	}
	result, err := reader.RecoverKeepLocal(ctx, project, project, "feature", env)
	if err != nil {
		probe, probeErr := reader.Commands.Run(ctx, execx.Request{Dir: project, Env: env, Name: "no-mistakes", Args: []string{"axi", "terminal-preflight", "--predecessor", os.Getenv("CFO_RECOVERY_NATIVE_RUN")}})
		t.Logf("native preflight: %s %s error=%v", probe.Stdout, probe.Stderr, probeErr)
		t.Fatal(err)
	}
	if result.RunID != os.Getenv("CFO_RECOVERY_NATIVE_RUN") || result.Head != os.Getenv("CFO_RECOVERY_NATIVE_HEAD") {
		t.Fatalf("recovered another source: %+v", result)
	}
	t.Logf("managed recovery: run=%s head=%s\n%s", result.RunID, result.Head, result.NativeOutput)
}
