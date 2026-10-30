package pipeline

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecoverKeepLocalAlignsAnchoredUnpublishedRebaseAfterCustodyReturn(t *testing.T) {
	requireFixtureTools(t, "git", "sqlite3")
	for _, scenario := range []string{
		"anchored follow-up",
		"gate alignment interrupted",
		"gate alignment lacks stale anchor",
		"dirty worktree",
		"unrelated follow-up",
		"missing recorded anchor",
		"symbolic recorded anchor",
		"missing native anchor",
		"published run",
		"native published mismatch",
		"wrong native run",
		"wrong branch",
		"gate branch moved",
	} {
		t.Run(scenario, func(t *testing.T) {
			// Arrange
			work := t.TempDir()
			runPipelineGit(t, work, "init", "--quiet", "--initial-branch=main")
			for _, setting := range [][2]string{{"user.name", "Fixture"}, {"user.email", "fixture@example.invalid"}, {"core.autocrlf", "false"}, {"commit.gpgsign", "false"}} {
				runPipelineGit(t, work, "config", setting[0], setting[1])
			}
			commit := func(file string) string {
				writeFixtureFile(t, filepath.Join(work, file), file+"\n")
				runPipelineGit(t, work, "add", file)
				runPipelineGit(t, work, "commit", "--quiet", "-m", file)
				return runPipelineGit(t, work, "rev-parse", "HEAD")
			}
			commit("base.txt")
			runPipelineGit(t, work, "switch", "--quiet", "-c", recoveryBranch)
			submitted := commit("feature.txt")
			runPipelineGit(t, work, "switch", "--quiet", "main")
			upstream := commit("upstream.txt")
			runPipelineGit(t, work, "switch", "--quiet", "--detach", submitted)
			runPipelineGit(t, work, "rebase", "--quiet", "main")
			preserved := runPipelineGit(t, work, "rev-parse", "HEAD")
			runPipelineGit(t, work, "switch", "--quiet", recoveryBranch)
			originalFollowUp := commit("follow-up.txt")
			runPipelineGit(t, work, "branch", "fixture/original-follow-up")
			runPipelineGit(t, work, "rebase", "--quiet", "--onto", preserved, submitted)
			if scenario == "unrelated follow-up" {
				runPipelineGit(t, work, "switch", "--quiet", "--detach", upstream)
				commit("unrelated.txt")
			}
			local := runPipelineGit(t, work, "rev-parse", "HEAD")
			isInterrupted := strings.HasPrefix(scenario, "gate alignment")
			shouldRecover := scenario == "anchored follow-up" || scenario == "gate alignment interrupted"
			if scenario == "dirty worktree" {
				writeFixtureFile(t, filepath.Join(work, "unfinished.txt"), "unfinished\n")
			}

			root := t.TempDir()
			bare := filepath.Join(root, "repos", recoveryRepo+".git")
			database := filepath.Join(root, "state.sqlite")
			runPipelineGit(t, root, "init", "--quiet", "--bare", bare)
			gate := submitted
			if scenario == "gate branch moved" {
				gate = upstream
			} else if isInterrupted {
				gate = local
			}
			runPipelineGit(t, work, "push", "--quiet", bare, gate+":refs/heads/"+recoveryBranch, preserved+":refs/fixture/preserved", submitted+":refs/fixture/submitted")
			if scenario == "gate alignment interrupted" {
				runPipelineGit(t, bare, "update-ref", "refs/no-mistakes/recovery/"+recoveryRun+"/gate", submitted)
			}
			recordedAnchor := "refs/no-mistakes/recovery/" + recoveryRun + "/recorded"
			if scenario != "missing recorded anchor" {
				runPipelineGit(t, bare, "update-ref", recordedAnchor, preserved)
			}
			if scenario == "symbolic recorded anchor" {
				runPipelineGit(t, bare, "symbolic-ref", recordedAnchor, "refs/fixture/preserved")
			}
			nativeAnchor := "refs/no-mistakes/recover/" + recoveryRun
			if scenario != "missing native anchor" {
				runPipelineGit(t, work, "update-ref", nativeAnchor, submitted)
			}
			pushed := "NULL"
			if scenario == "published run" {
				pushed = sqlString(submitted)
			}
			writeFixtureSQLite(t, database, `CREATE TABLE repos(id TEXT,working_path TEXT);
CREATE TABLE runs(id TEXT,repo_id TEXT,branch TEXT,status TEXT,head_sha TEXT,submitted_head_sha TEXT,last_pushed_sha TEXT,custody_returned_at INTEGER,created_at INTEGER,updated_at INTEGER);
CREATE TABLE steps(run_id TEXT,step TEXT,attempt INTEGER,status TEXT);
INSERT INTO repos VALUES(`+sqlString(recoveryRepo)+`,`+sqlString(filepath.ToSlash(work))+`);
INSERT INTO runs VALUES(`+sqlString(recoveryRun)+`,`+sqlString(recoveryRepo)+`,`+sqlString(recoveryBranch)+`,'failed',`+sqlString(submitted)+`,`+sqlString(submitted)+`,`+pushed+`,1,1,1);
INSERT INTO steps VALUES(`+sqlString(recoveryRun)+`,'review',3,'passed');
INSERT INTO steps VALUES(`+sqlString(recoveryRun)+`,'test',1,'failed');`)
			runner := &recordedHeadRunner{database: database, work: work, run: recoveryRun, bare: bare}
			if scenario == "native published mismatch" {
				runner.pushedHead = submitted
			}
			if scenario == "wrong native run" {
				runner.syncRun = "another-run"
			}
			branch := recoveryBranch
			if scenario == "wrong branch" {
				branch = "fix/another-task"
			}
			reader := Reader{Root: root, Commands: runner}
			before, nativeOutput, err := reader.readRecoverySync(context.Background(), work, nil)
			expectedSafety := "recovery_required"
			if isInterrupted {
				expectedSafety = "gate_ready"
			}
			if err != nil || before.State != "custody_returned" || before.Safety != expectedSafety || !strings.Contains(string(nativeOutput), "relation: diverged") || before.Local.Head != local || before.Pipeline.CurrentHead != submitted || !isInterrupted && before.NextAction.Command != "no-mistakes axi sync --adopt-published" {
				t.Fatalf("native reproduction: %+v, %v", before, err)
			}
			refsBefore := runPipelineGit(t, bare, "show-ref")

			// Act
			result, recoverErr := reader.RecoverKeepLocal(context.Background(), work, work, branch, nil)

			// Assert
			if shouldRecover && recoverErr != nil {
				t.Fatalf("recover anchored unpublished rebase: %v", recoverErr)
			}
			if head := runPipelineGit(t, work, "rev-parse", "HEAD"); head != local {
				t.Fatalf("local head moved from %s to %s", local, head)
			}
			if head := runPipelineGit(t, work, "rev-parse", "fixture/original-follow-up"); head != originalFollowUp {
				t.Fatalf("original follow-up history moved to %s", head)
			}
			if runner.recoveries != 0 {
				t.Fatal("custody was recovered a second time")
			}
			row, err := exec.Command("sqlite3", database, "SELECT id||' '||repo_id||' '||branch||' '||status||' '||head_sha||' '||submitted_head_sha||' '||COALESCE(last_pushed_sha,'')||' '||custody_returned_at FROM runs").Output()
			if err != nil {
				t.Fatal(err)
			}
			steps, err := exec.Command("sqlite3", database, "SELECT step||' '||attempt||' '||status FROM steps ORDER BY step").Output()
			if err != nil || strings.TrimSpace(strings.ReplaceAll(string(steps), "\r\n", "\n")) != "review 3 passed\ntest 1 failed" {
				t.Fatalf("review or test history changed: %q, %v", steps, err)
			}
			expectedHead := submitted
			if shouldRecover {
				if recoverErr != nil || result.RunID != recoveryRun || result.Head != local {
					t.Fatalf("recover anchored unpublished rebase: %+v, %v", result, recoverErr)
				}
				expectedHead = local
				if head := runPipelineGit(t, bare, "rev-parse", "refs/heads/"+branch); head != local {
					t.Fatalf("gate head = %s, want %s", head, local)
				}
				if head := runPipelineGit(t, bare, "rev-parse", "refs/no-mistakes/recovery/"+recoveryRun+"/gate"); head != submitted {
					t.Fatalf("submitted history anchor = %s, want %s", head, submitted)
				}
				if head := runPipelineGit(t, bare, "rev-parse", recordedAnchor); head != preserved {
					t.Fatalf("preserved pipeline anchor = %s, want %s", head, preserved)
				}
				if _, retryErr := reader.RecoverKeepLocal(context.Background(), work, work, branch, nil); retryErr != nil {
					t.Fatalf("aligned retry: %v", retryErr)
				}
				if out, err := exec.Command("git", "-C", work, "push", "--dry-run", bare, branch+":refs/heads/"+branch).CombinedOutput(); err != nil {
					t.Fatalf("next gate submission refused: %s, %v", out, err)
				}
			} else if recoverErr == nil || runPipelineGit(t, bare, "show-ref") != refsBefore {
				t.Fatalf("unsafe recovery changed refs or succeeded: %v", recoverErr)
			}
			expectedPushed := ""
			if scenario == "published run" {
				expectedPushed = submitted
			}
			expectedRow := strings.Join([]string{recoveryRun, recoveryRepo, recoveryBranch, "failed", expectedHead, expectedHead, expectedPushed, "1"}, " ")
			if strings.TrimSpace(string(row)) != expectedRow {
				t.Fatalf("run identity or heads: %q, want %q", row, expectedRow)
			}
		})
	}
}
