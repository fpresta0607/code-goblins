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
		name           string
		olderStatus    string
		isOlderPushed  bool
		isHeldByNewer  bool
		isDuplicateMet bool
	}{
		{name: "cancelled never-pushed run whose head the published run holds", olderStatus: "cancelled", isHeldByNewer: true, isDuplicateMet: true},
		{name: "published run that does not hold the cancelled head", olderStatus: "cancelled"},
		{name: "older failed run", olderStatus: "failed", isHeldByNewer: true},
		{name: "older cancelled run that pushed", olderStatus: "cancelled", isOlderPushed: true, isHeldByNewer: true},
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
			runPipelineGit(t, work, "push", "--quiet", bare, published+":refs/heads/"+branch, submitted+":refs/fixture/older")
			olderPushed := "NULL"
			if test.isOlderPushed {
				olderPushed = sqlString(submitted)
			}
			sql := `CREATE TABLE repos(id TEXT,working_path TEXT);
CREATE TABLE runs(id TEXT,repo_id TEXT,branch TEXT,status TEXT,head_sha TEXT,submitted_head_sha TEXT,last_pushed_sha TEXT,custody_returned_at INTEGER,terminal_head_verified_at INTEGER,created_at INTEGER,updated_at INTEGER);
INSERT INTO repos VALUES(` + sqlString(repo) + `,` + sqlString(filepath.ToSlash(work)) + `);
INSERT INTO runs VALUES(` + sqlString(older) + `,` + sqlString(repo) + `,` + sqlString(branch) + `,` + sqlString(test.olderStatus) + `,` + sqlString(submitted) + `,` + sqlString(submitted) + `,` + olderPushed + `,NULL,NULL,1,1);
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

func TestRecoverKeepLocalNamesAFreshRunOnlyForARunThatSpentNoRepairRound(t *testing.T) {
	for _, test := range []struct {
		name         string
		repairRounds int
		want         string
	}{
		{name: "no repair round spent", want: "a fresh goblins pipeline run is the expected path"},
		{name: "repair rounds spent", repairRounds: 2, want: "spent 2 repair rounds, which a fresh run would start over: ask the CEO before starting one"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange: custody already returned, and native branch sync no
			// longer answers for the run.
			runner := &recoveryRunner{
				scenario: recoveryScenario{
					run:       "01M761B9CBW4RHC6KC0NH6ETG5",
					repo:      recoveryRepo,
					branch:    "fix/failed-after-fixes",
					recorded:  "c5fcb965b84750629f8a87f35ee571a128ec7e4e",
					submitted: "0c61c8d1bb5145fb3dad57fde7f0e139a0c98a83",
				},
				custodyReturned: true,
				repairRounds:    test.repairRounds,
			}
			reader := newRecoveryReader(t, runner)

			// Act
			_, err := reader.RecoverKeepLocal(context.Background(), "project", "worktree", runner.scenario.branch, nil)

			// Assert
			if err == nil || !strings.Contains(err.Error(), "recorded custody return does not match native branch state") || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("refusal: %v, want it to say %q", err, test.want)
			}
		})
	}
}
