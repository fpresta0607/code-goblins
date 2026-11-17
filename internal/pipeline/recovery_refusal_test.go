package pipeline

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/pipeline/pipelinetest"
)

// nativeRefusingRunner runs git and sqlite3 for real and fails any native
// command, so a test proves a refusal came before the engine was asked.
type nativeRefusingRunner struct{ nativeCalls int }

func (r *nativeRefusingRunner) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	if request.Name == "no-mistakes" {
		r.nativeCalls++
		return execx.Result{}, errors.New("native command must not run")
	}
	return pipelinetest.FixtureCommands{Runner: execx.OSRunner{}}.Run(ctx, request)
}

func TestRecoverKeepLocalNamesACancelledDuplicateBesideTheNewerPublishedRun(t *testing.T) {
	for _, tool := range []string{"git", "sqlite3"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " CLI not available")
		}
	}
	const (
		older  = "01M7619D5A19FAT3719273HXHQ"
		newer  = "01M761G7KWAJE99E4B16Q819BC"
		repo   = "5c0de9061b11"
		branch = "fix/cancelled-duplicate"
	)
	for _, test := range []struct {
		name                    string
		olderStatus             string
		isOlderPushed           bool
		isOlderHeadMoved        bool
		isOlderTerminalVerified bool
		isOlderCustodyReturned  bool
		isOlderHeadAbsent       bool
		isOlderOnOtherBranch    bool
		isOlderInOtherRepo      bool
		isPublishedHeadAbsent   bool
		isHeldByNewer           bool
		isDuplicateMet          bool
	}{
		{name: "cancelled never-pushed run whose head the published run holds", olderStatus: "cancelled", isHeldByNewer: true, isDuplicateMet: true},
		{name: "published run that does not hold the cancelled head", olderStatus: "cancelled"},
		{name: "older failed run", olderStatus: "failed", isHeldByNewer: true},
		{name: "older cancelled run that pushed", olderStatus: "cancelled", isOlderPushed: true, isHeldByNewer: true},
		{name: "older cancelled run whose head is not its submitted head", olderStatus: "cancelled", isOlderHeadMoved: true, isHeldByNewer: true},
		{name: "older cancelled run whose terminal head was verified", olderStatus: "cancelled", isOlderTerminalVerified: true, isHeldByNewer: true},
		{name: "older cancelled run whose custody returned", olderStatus: "cancelled", isOlderCustodyReturned: true, isHeldByNewer: true},
		{name: "cancelled head the gate no longer has", olderStatus: "cancelled", isOlderHeadAbsent: true},
		{name: "published head the gate no longer has", olderStatus: "cancelled", isPublishedHeadAbsent: true, isHeldByNewer: true},
		{name: "cancelled run of another branch the published run holds", olderStatus: "cancelled", isOlderOnOtherBranch: true, isHeldByNewer: true},
		{name: "cancelled run of another repository the published run holds", olderStatus: "cancelled", isOlderInOtherRepo: true, isHeldByNewer: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange: two starts of one branch, the older cancelled at its
			// submitted head and the newer completed and published.
			work := filepath.Join(t.TempDir(), "work")
			runPipelineGit(t, filepath.Dir(work), "init", "--quiet", "--initial-branch=main", work)
			for _, setting := range [][2]string{{"user.name", "Pipeline Test"}, {"user.email", "pipeline@example.com"}, {"core.autocrlf", "false"}, {"commit.gpgsign", "false"}} {
				runPipelineGit(t, work, "config", setting[0], setting[1])
			}
			commit := func(file string) string {
				if err := os.WriteFile(filepath.Join(work, file), []byte(file+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				runPipelineGit(t, work, "add", file)
				runPipelineGit(t, work, "commit", "--quiet", "-m", file)
				return runPipelineGit(t, work, "rev-parse", "HEAD")
			}
			base := commit("base.txt")
			runPipelineGit(t, work, "switch", "--quiet", "-c", branch)
			submitted := commit("feature.txt")
			if !test.isHeldByNewer {
				runPipelineGit(t, work, "switch", "--quiet", "--detach", base)
			}
			published := commit("gate-fix.txt")
			runPipelineGit(t, work, "switch", "--quiet", "--force", branch)
			runPipelineGit(t, work, "reset", "--quiet", "--hard", published)

			root := t.TempDir()
			database := filepath.Join(root, "state.sqlite")
			bare := filepath.Join(root, "repos", repo+".git")
			runPipelineGit(t, root, "init", "--quiet", "--bare", bare)
			var refspecs []string
			if !test.isPublishedHeadAbsent {
				refspecs = append(refspecs, published+":refs/heads/"+branch)
			}
			if !test.isOlderHeadAbsent {
				refspecs = append(refspecs, submitted+":refs/fixture/older")
			}
			runPipelineGit(t, work, append([]string{"push", "--quiet", bare}, refspecs...)...)
			olderHead := submitted
			if test.isOlderHeadMoved {
				olderHead = base
			}
			olderBranch, olderRepo := branch, repo
			if test.isOlderOnOtherBranch {
				olderBranch = "fix/stacked-parent"
			}
			if test.isOlderInOtherRepo {
				olderRepo = "0a1b2c3d4e5f"
			}
			olderPushed, olderTerminalVerified, olderCustodyReturned := "NULL", "NULL", "NULL"
			if test.isOlderPushed {
				olderPushed = sqlString(submitted)
			}
			if test.isOlderTerminalVerified {
				olderTerminalVerified = "1"
			}
			if test.isOlderCustodyReturned {
				olderCustodyReturned = "1"
			}
			sql := `CREATE TABLE repos(id TEXT,working_path TEXT);
CREATE TABLE runs(id TEXT,repo_id TEXT,branch TEXT,status TEXT,head_sha TEXT,submitted_head_sha TEXT,last_pushed_sha TEXT,custody_returned_at INTEGER,terminal_head_verified_at INTEGER,created_at INTEGER,updated_at INTEGER);
INSERT INTO repos VALUES(` + sqlString(repo) + `,` + sqlString(filepath.ToSlash(work)) + `);
INSERT INTO runs VALUES(` + sqlString(older) + `,` + sqlString(olderRepo) + `,` + sqlString(olderBranch) + `,` + sqlString(test.olderStatus) + `,` + sqlString(olderHead) + `,` + sqlString(submitted) + `,` + olderPushed + `,` + olderCustodyReturned + `,` + olderTerminalVerified + `,1,1);
INSERT INTO runs VALUES(` + sqlString(newer) + `,` + sqlString(repo) + `,` + sqlString(branch) + `,'completed',` + sqlString(published) + `,` + sqlString(submitted) + `,` + sqlString(published) + `,NULL,2,2,2);`
			if out, err := exec.Command("sqlite3", database, sql).CombinedOutput(); err != nil {
				t.Fatalf("fixture: %s %v", out, err)
			}
			runner := &nativeRefusingRunner{}
			reader := Reader{Root: root, Commands: runner}

			// Act
			_, err := reader.RecoverKeepLocal(context.Background(), work, work, branch, nil)

			// Assert
			if err == nil || runner.nativeCalls != 0 {
				t.Fatalf("recovery was not refused before the engine: error=%v native calls=%d", err, runner.nativeCalls)
			}
			named := strings.Contains(err.Error(), older) && strings.Contains(err.Error(), newer) && strings.Contains(err.Error(), published) && strings.Contains(err.Error(), "no-mistakes/issues/335")
			if named != test.isDuplicateMet {
				t.Fatalf("refusal names the cancelled duplicate %t, want %t: %v", named, test.isDuplicateMet, err)
			}
			if !test.isDuplicateMet && !strings.Contains(err.Error(), "latest run is not an unpublished failed or cancelled custody recovery") {
				t.Fatalf("refusal: %v", err)
			}
		})
	}
}

// stateRecoveryRunner reads the state database for real and answers every
// other command from the recovery mock.
type stateRecoveryRunner struct{ *recoveryRunner }

func (r stateRecoveryRunner) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	if request.Name == "sqlite3" {
		return pipelinetest.FixtureCommands{Runner: execx.OSRunner{}}.Run(ctx, request)
	}
	return r.recoveryRunner.Run(ctx, request)
}

func TestRecoverKeepLocalNamesAFreshRunOnlyForARunThatSpentNoRepairRound(t *testing.T) {
	const (
		run      = "01M761B9CBW4RHC6KC0NH6ETG5"
		otherRun = "01M761C4QH0WZ3T2W8V1M2XK7P"
		branch   = "fix/failed-after-fixes"
	)
	for _, test := range []struct {
		name           string
		repairTriggers []string
		want           string
	}{
		{name: "no repair round spent", want: "it spent no repair round, so a fresh goblins pipeline run is the expected path"},
		{name: "one repair round spent", repairTriggers: []string{"auto_fix"}, want: "spent 1 repair round, which a fresh run would start over: ask the CEO before starting one"},
		{name: "automatic and user repair rounds spent", repairTriggers: []string{"auto_fix", "user_fix"}, want: "spent 2 repair rounds, which a fresh run would start over: ask the CEO before starting one"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange: custody already returned, native branch sync no longer
			// answers for the run, and the ledger holds the run's initial
			// rounds and another run's repair round beside its own repairs.
			runner := &recoveryRunner{
				scenario: recoveryScenario{
					run:       run,
					repo:      recoveryRepo,
					branch:    branch,
					recorded:  "c5fcb965b84750629f8a87f35ee571a128ec7e4e",
					submitted: "0c61c8d1bb5145fb3dad57fde7f0e139a0c98a83",
				},
				custodyReturned: true,
			}
			reader := Reader{Root: t.TempDir(), Commands: stateRecoveryRunner{runner}}
			sql := `CREATE TABLE repos(id TEXT,working_path TEXT);
CREATE TABLE runs(id TEXT,repo_id TEXT,branch TEXT,status TEXT,head_sha TEXT,submitted_head_sha TEXT,last_pushed_sha TEXT,custody_returned_at INTEGER,terminal_head_verified_at INTEGER,created_at INTEGER,updated_at INTEGER);
CREATE TABLE step_results(id TEXT,run_id TEXT,step_name TEXT);
CREATE TABLE step_rounds(id TEXT,step_result_id TEXT,round INTEGER,trigger_type TEXT);
INSERT INTO repos VALUES(` + sqlString(recoveryRepo) + `,'project');
INSERT INTO runs VALUES(` + sqlString(run) + `,` + sqlString(recoveryRepo) + `,` + sqlString(branch) + `,'failed',` + sqlString(runner.scenario.recorded) + `,` + sqlString(runner.scenario.submitted) + `,NULL,1,NULL,1,1);
INSERT INTO step_results VALUES('review',` + sqlString(run) + `,'review'),('test',` + sqlString(run) + `,'test'),('other-review',` + sqlString(otherRun) + `,'review');
INSERT INTO step_rounds VALUES('review-1','review',1,'initial'),('test-1','test',1,'initial'),('other-review-2','other-review',2,'auto_fix');`
			for index, trigger := range test.repairTriggers {
				round := index + 2
				sql += fmt.Sprintf("\nINSERT INTO step_rounds VALUES('review-%d','review',%d,%s);", round, round, sqlString(trigger))
			}
			writeFixtureSQLite(t, filepath.Join(reader.Root, "state.sqlite"), sql)

			// Act
			_, err := reader.RecoverKeepLocal(context.Background(), "project", "worktree", branch, nil)

			// Assert
			if err == nil || !strings.Contains(err.Error(), "recorded custody return does not match native branch state; run "+run) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("refusal: %v, want it to say %q", err, test.want)
			}
		})
	}
}
