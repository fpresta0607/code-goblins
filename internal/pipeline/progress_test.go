package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type custodyProgressRunner struct {
	progress Progress
	native   string
	requests []execx.Request
	recovery *recoveryRunner
}

func (r *custodyProgressRunner) Run(ctx context.Context, req execx.Request) (execx.Result, error) {
	r.requests = append(r.requests, req)
	if req.Name == "sqlite3" {
		if strings.Contains(req.Args[len(req.Args)-1], "FROM step_results") {
			return execx.Result{Stdout: []byte("[]")}, nil
		}
		data, _ := json.Marshal([]Progress{r.progress})
		return execx.Result{Stdout: data}, nil
	}
	if req.Name == "no-mistakes" {
		if strings.Join(req.Args, " ") != "axi sync --check" {
			return execx.Result{}, fmt.Errorf("unexpected mutation %v", req.Args)
		}
		return execx.Result{Stdout: []byte(fmt.Sprintf("branch_sync:\n  state: %s\n  safety: %s\n  local: {head: %s, clean: true}\n  pipeline: {run: %s, submitted_head: %s, current_head: %s}\n", r.native, r.native, r.progress.Head, r.progress.RunID, r.progress.SubmittedHead, r.progress.Head))}, nil
	}
	return r.recovery.Run(ctx, req)
}

func TestFeedbackRejectsUnrecoveredTerminalCustody(t *testing.T) {
	for _, status := range []string{"failed", "cancelled"} {
		for _, returned := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/returned=%t", status, returned), func(t *testing.T) {
				runner := &custodyProgressRunner{progress: Progress{RunID: recoveryRun, RepoID: recoveryRepo, Status: status, Head: recoveryRecorded, SubmittedHead: recoveryRecorded}, native: "pipeline_owned", recovery: &recoveryRunner{nativeAnchor: true, nativeAnchorHead: recoveryRecorded, nativeAnchorWorktree: true, nativeWorktreeHead: recoveryRecorded}}
				if returned {
					runner.progress.CustodyReturned = 1
					runner.native = "custody_returned"
				}
				r := newRecoveryReader(t, runner)
				err := r.CanSteer(context.Background(), "C:/project", "C:/work", recoveryBranch)
				if (err == nil) != returned {
					t.Fatalf("returned=%t err=%v requests=%+v", returned, err, runner.requests)
				}
				if returned {
					runner.native = "pipeline_owned"
					if err := r.CanSteer(context.Background(), "C:/project", "C:/work", recoveryBranch); err == nil {
						t.Fatal("timestamp alone bypassed custody")
					}
				}
			})
		}
	}
}

func TestProgressReadsExactLatestBranchFromSQLite(t *testing.T) {
	root := t.TempDir()
	database := filepath.Join(root, "state.sqlite")
	sql := `CREATE TABLE repos(id TEXT,working_path TEXT);
CREATE TABLE runs(id TEXT,repo_id TEXT,branch TEXT,created_at INTEGER,status TEXT,head_sha TEXT,submitted_head_sha TEXT,review_approved_head_sha TEXT,last_pushed_sha TEXT,pr_url TEXT,pr_state TEXT,ci_ready_at INTEGER,terminal_head_verified_at INTEGER,custody_returned_at INTEGER);
CREATE TABLE step_results(run_id TEXT,step_name TEXT,status TEXT,step_order INTEGER);
INSERT INTO repos VALUES('repo','C:\project');
INSERT INTO runs(id,repo_id,branch,created_at,status,head_sha) VALUES('old','repo','feat',1,'completed','old'),('latest','repo','feat',2,'running','latest'),('other','repo','other',3,'completed','other');
INSERT INTO step_results VALUES('latest','review','awaiting_approval',1);`
	result, err := (execx.OSRunner{}).Run(context.Background(), execx.Request{Name: "sqlite3", Args: []string{database, sql}})
	if err != nil {
		t.Skipf("sqlite3 unavailable: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatal(string(result.Stderr))
	}
	r := Reader{Root: root, Commands: execx.OSRunner{}}
	p, err := r.Progress(context.Background(), "C:/project", "feat")
	if err != nil || p.RunID != "latest" || len(p.Steps) != 1 {
		t.Fatalf("wrong branch evidence %+v %v", p, err)
	}
	if _, err := r.Progress(context.Background(), "C:/other", "feat"); err == nil {
		t.Fatal("wrong project matched")
	}
	if _, err := os.Stat(database); err != nil {
		t.Fatal(err)
	}
}

func TestProgressRequiresReviewTestsCommitPushAndPRAtExactHead(t *testing.T) {
	p := Progress{Head: "abc", ReviewedHead: "abc", PushedHead: "abc", PR: "https://github.com/a/b/pull/1", CIReady: 1, Status: "running"}
	for _, name := range []string{"review", "test", "lint", "document", "push", "pr"} {
		p.Steps = append(p.Steps, ProgressStep{Name: name, Status: "completed"})
	}
	if !p.Ready("abc") {
		t.Fatal("complete evidence refused")
	}
	if p.Ready("def") {
		t.Fatal("stale evidence accepted")
	}
	for i := range p.Steps {
		p.Steps[i].Status = "skipped"
		if p.Ready("abc") {
			t.Fatalf("skipped %s accepted", p.Steps[i].Name)
		}
		p.Steps[i].Status = "completed"
	}
	p.ReviewedHead = "old"
	if p.Ready("abc") {
		t.Fatal("stale review accepted")
	}
}
