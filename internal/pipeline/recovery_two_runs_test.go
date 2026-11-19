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

// earlierOwnerNative runs git and sqlite3 for real and stands in for the
// native engine of #2757: its branch sync names the earlier failed run as the
// branch's owner, since the later cancelled run already returned custody, and
// its recovery returns that run's custody at its recorded head, as native
// recovery of a run that failed after the gate committed its review fixes
// does.
type earlierOwnerNative struct {
	t                             *testing.T
	database, bare, worktree, run string
	isReturned                    bool
	recoveries                    int
}

func (n *earlierOwnerNative) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	if request.Name != "no-mistakes" {
		return pipelinetest.FixtureCommands{Runner: execx.OSRunner{}}.Run(ctx, request)
	}
	switch strings.Join(request.Args, " ") {
	case "axi sync --check":
		var heads [2]string
		for index, column := range []string{"submitted_head_sha", "head_sha"} {
			heads[index] = n.sqlite("SELECT " + column + " FROM runs WHERE id=" + sqlString(n.run))
		}
		local := runPipelineGit(n.t, n.worktree, "rev-parse", "HEAD")
		if n.isReturned {
			return execx.Result{Stdout: []byte("branch_sync:\n  state: custody_returned\n  safety: custody_returned\n  local:\n    head: " + local + "\n    clean: true\n  pipeline:\n    run: \"" + n.run + "\"\n    submitted_head: " + heads[0] + "\n    current_head: " + heads[1] + "\n")}, nil
		}
		return execx.Result{ExitCode: 1, Stdout: []byte("branch_sync:\n  state: pipeline_owned\n  safety: blocked_pipeline_owned_recoverable\n  local:\n    head: " + local + "\n    clean: true\n  pipeline:\n    run: \"" + n.run + "\"\n    submitted_head: " + heads[0] + "\n    current_head: " + heads[1] + "\n  next_action:\n    code: recover_custody\n    command: no-mistakes axi sync --recover\n")}, nil
	case "axi sync --recover":
		n.recoveries++
		recorded := n.sqlite("SELECT head_sha FROM runs WHERE id=" + sqlString(n.run))
		runPipelineGit(n.t, n.bare, "update-ref", "refs/no-mistakes/recover/"+n.run, recorded)
		n.sqlite("UPDATE runs SET custody_returned_at=3 WHERE id=" + sqlString(n.run))
		n.isReturned = true
		return execx.Result{Stdout: []byte("custody returned\n")}, nil
	}
	return execx.Result{}, errors.New("unexpected native command: " + strings.Join(request.Args, " "))
}

func (n *earlierOwnerNative) sqlite(sql string) string {
	n.t.Helper()
	output, err := exec.Command("sqlite3", n.database, sql).CombinedOutput()
	if err != nil {
		n.t.Fatalf("sqlite3 %s: %s %v", sql, output, err)
	}
	return strings.TrimSpace(string(output))
}

func TestRecoverKeepLocalRecoversTheEarlierRunNativeStillNamesAfterALaterRunReturnedCustody(t *testing.T) {
	for _, tool := range []string{"git", "sqlite3"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " CLI not available")
		}
	}
	const (
		failed    = "01M7RE22W7Z0TPDT9XFWGNFYK5"
		cancelled = "01M7RG4275G50REJA5FRHFTSF5"
		repo      = "a3a29a05a5f9"
		branch    = "fix/2099-key-alarm-images"
	)
	for _, test := range []struct {
		name              string
		isLocalAtRecorded bool
		wantRecoveries    int
	}{
		{name: "task branch at the submitted head is told to take the gate's review fix"},
		{name: "task branch at the gate's review fix returns the failed run's custody", isLocalAtRecorded: true, wantRecoveries: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange: the failed run committed a review fix on top of the
			// submitted head and failed in Test, then a later run of the same
			// branch was cancelled and has already returned custody, so
			// native branch sync names the failed run again.
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
			commit("base.txt")
			runPipelineGit(t, work, "switch", "--quiet", "-c", branch)
			submitted := commit("fix.txt")
			recorded := commit("review-fix.txt")
			if !test.isLocalAtRecorded {
				runPipelineGit(t, work, "reset", "--quiet", "--hard", submitted)
			}

			root := t.TempDir()
			database := filepath.Join(root, "state.sqlite")
			bare := filepath.Join(root, "repos", repo+".git")
			runPipelineGit(t, root, "init", "--quiet", "--bare", bare)
			runPipelineGit(t, work, "push", "--quiet", bare, submitted+":refs/heads/"+branch, recorded+":refs/fixture/review-fix")
			sql := `CREATE TABLE repos(id TEXT,working_path TEXT);
CREATE TABLE runs(id TEXT,repo_id TEXT,branch TEXT,status TEXT,head_sha TEXT,submitted_head_sha TEXT,last_pushed_sha TEXT,custody_returned_at INTEGER,terminal_head_verified_at INTEGER,review_approved_head_sha TEXT,created_at INTEGER,updated_at INTEGER);
CREATE TABLE step_results(id TEXT,run_id TEXT,step_name TEXT);
CREATE TABLE step_rounds(id TEXT,step_result_id TEXT,round INTEGER,trigger_type TEXT);
CREATE TABLE validation_admissions(id TEXT,child_id TEXT,predecessor_id TEXT,event_json TEXT);
INSERT INTO repos VALUES(` + sqlString(repo) + `,` + sqlString(filepath.ToSlash(work)) + `);
INSERT INTO runs VALUES(` + sqlString(failed) + `,` + sqlString(repo) + `,` + sqlString(branch) + `,'failed',` + sqlString(recorded) + `,` + sqlString(submitted) + `,NULL,NULL,NULL,` + sqlString(recorded) + `,1,1);
INSERT INTO runs VALUES(` + sqlString(cancelled) + `,` + sqlString(repo) + `,` + sqlString(branch) + `,'cancelled',` + sqlString(submitted) + `,` + sqlString(submitted) + `,NULL,2,NULL,NULL,2,2);
INSERT INTO step_results VALUES('review',` + sqlString(failed) + `,'review');
INSERT INTO step_rounds VALUES('review-1','review',1,'initial'),('review-2','review',2,'user_fix');`
			if out, err := exec.Command("sqlite3", database, sql).CombinedOutput(); err != nil {
				t.Fatalf("fixture: %s %v", out, err)
			}
			native := &earlierOwnerNative{t: t, database: database, bare: bare, worktree: work, run: failed}
			reader := Reader{Root: root, Commands: native}

			// Act
			result, err := reader.RecoverKeepLocal(context.Background(), work, work, branch, nil)

			// Assert
			if native.recoveries != test.wantRecoveries {
				t.Fatalf("native recovered %d times, want %d: %v", native.recoveries, test.wantRecoveries, err)
			}
			if !test.isLocalAtRecorded {
				want := fmt.Sprintf("put the clean task branch at the recorded head %s and recover again", recorded)
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("recovery of the earlier owner: %v, want it to say %q", err, want)
				}
				return
			}
			if err != nil {
				t.Fatalf("recovery of the earlier owner: %v", err)
			}
			if result.RunID != failed || result.Head != recorded || result.GateHead != recorded {
				t.Fatalf("result: %+v, want run %s at %s", result, failed, recorded)
			}
			if gate := runPipelineGit(t, bare, "rev-parse", "refs/heads/"+branch); gate != recorded {
				t.Fatalf("gate branch at %s, want the recorded head %s", gate, recorded)
			}
		})
	}
}
