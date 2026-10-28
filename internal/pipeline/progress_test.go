package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type custodyProgressRunner struct {
	progress Progress
	native   string
	isDirty  bool
	requests []execx.Request
	recovery *recoveryRunner
}

// checks is how many native custody checks the runner was asked to start.
func (r *custodyProgressRunner) checks() int {
	checks := 0
	for _, req := range r.requests {
		if req.Name == "no-mistakes" {
			checks++
		}
	}
	return checks
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
		return execx.Result{Stdout: []byte(fmt.Sprintf("branch_sync:\n  state: %s\n  safety: %s\n  local: {head: %s, clean: %t}\n  pipeline: {run: %s, submitted_head: %s, current_head: %s}\n", r.native, r.native, r.progress.Head, !r.isDirty, r.progress.RunID, r.progress.SubmittedHead, r.progress.Head))}, nil
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

// A board terminal view rechecks custody every few seconds, and each query is
// a sqlite3 process, so the check asks for the branch's latest run alone and
// never for its steps, which custody does not depend on.
func TestCanSteerReadsOnlyTheLatestRun(t *testing.T) {
	for _, status := range []string{"completed", "running"} {
		t.Run(status, func(t *testing.T) {
			runner := &custodyProgressRunner{progress: Progress{RunID: recoveryRun, RepoID: recoveryRepo, Status: status, Head: recoveryRecorded}}
			r := newRecoveryReader(t, runner)
			err := r.CanSteer(context.Background(), "C:/project", "C:/work", recoveryBranch)
			if (err == nil) != (status == "completed") {
				t.Fatalf("err=%v, want custody only for the completed run", err)
			}
			if len(runner.requests) != 1 || runner.requests[0].Name != "sqlite3" || strings.Contains(runner.requests[0].Args[len(runner.requests[0].Args)-1], "step_results") {
				t.Fatalf("requests=%+v, want one query of the latest run", runner.requests)
			}
		})
	}
}

// An open board terminal view asks for custody every five seconds, and each
// ask started the native check and its update check while nothing changed
// (#1678). The check runs once for each record of the branch's newest run.
func TestRepeatedCustodyReadsCheckOncePerRecordOfTheNewestRun(t *testing.T) {
	for _, test := range []struct {
		name      string
		native    string
		submitted string
		returned  int64
		isClear   bool
	}{
		{"the gate still holds an unrecovered run", "pipeline_owned", recoveryPreserved, 0, false},
		{"the run released its unmoved head", "user_owned", recoveryRecorded, 0, true},
		{"the run returned custody", "custody_returned", recoveryPreserved, 1, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			runner := &custodyProgressRunner{progress: Progress{RunID: recoveryRun, RepoID: recoveryRepo, Status: "cancelled", Head: recoveryRecorded, SubmittedHead: test.submitted, CustodyReturned: test.returned}, native: test.native, recovery: &recoveryRunner{nativeAnchor: true, nativeAnchorHead: recoveryRecorded, nativeAnchorWorktree: true, nativeWorktreeHead: recoveryRecorded}}
			r := newRecoveryReader(t, runner)
			r.Custody = NewCustodyCache()

			// Act
			var verdicts []error
			for range 5 {
				verdicts = append(verdicts, r.CanSteer(context.Background(), "C:/project", "C:/work", recoveryBranch))
			}

			// Assert
			for _, err := range verdicts {
				if (err == nil) != test.isClear || err != nil && !errors.Is(err, ErrCustodyNotReturned) {
					t.Fatalf("verdicts=%v, want clear=%t each time", verdicts, test.isClear)
				}
			}
			if checks := runner.checks(); checks != 1 {
				t.Fatalf("five reads of one record started %d native checks, want 1", checks)
			}
		})
	}
}

// A goblin that recovers custody changes its run's record, and the next read
// checks again and lets it type.
func TestACustodyReadChecksAgainWhenTheNewestRunChanges(t *testing.T) {
	// Arrange
	runner := &custodyProgressRunner{progress: Progress{RunID: recoveryRun, RepoID: recoveryRepo, Status: "cancelled", Head: recoveryRecorded, SubmittedHead: recoveryPreserved}, native: "pipeline_owned", recovery: &recoveryRunner{nativeAnchor: true, nativeAnchorHead: recoveryRecorded, nativeAnchorWorktree: true, nativeWorktreeHead: recoveryRecorded}}
	r := newRecoveryReader(t, runner)
	r.Custody = NewCustodyCache()
	held := r.CanSteer(context.Background(), "C:/project", "C:/work", recoveryBranch)

	// Act
	runner.progress.CustodyReturned = 1
	runner.native = "custody_returned"
	returned := r.CanSteer(context.Background(), "C:/project", "C:/work", recoveryBranch)
	again := r.CanSteer(context.Background(), "C:/project", "C:/work", recoveryBranch)

	// Assert
	if !errors.Is(held, ErrCustodyNotReturned) || returned != nil || again != nil {
		t.Fatalf("held=%v returned=%v again=%v, want a hold and then clear", held, returned, again)
	}
	if checks := runner.checks(); checks != 2 {
		t.Fatalf("two records started %d native checks, want 2", checks)
	}
}

// Custody is the native engine's for the run, so a goblin's uncommitted work
// after its gate let go does not hand the branch back to the gate.
func TestAReleasedBranchStaysTheGoblinsWhileItsWorktreeIsDirty(t *testing.T) {
	for _, test := range []struct {
		native    string
		submitted string
		returned  int64
	}{
		{"user_owned", recoveryRecorded, 0},
		{"custody_returned", recoveryPreserved, 1},
	} {
		t.Run(test.native, func(t *testing.T) {
			// Arrange
			runner := &custodyProgressRunner{progress: Progress{RunID: recoveryRun, RepoID: recoveryRepo, Status: "failed", Head: recoveryRecorded, SubmittedHead: test.submitted, CustodyReturned: test.returned}, native: test.native, isDirty: true, recovery: &recoveryRunner{nativeAnchor: true, nativeAnchorHead: recoveryRecorded, nativeAnchorWorktree: true, nativeWorktreeHead: recoveryRecorded}}

			// Act
			err := newRecoveryReader(t, runner).CanSteer(context.Background(), "C:/project", "C:/work", recoveryBranch)

			// Assert
			if err != nil {
				t.Fatalf("a released branch with a dirty worktree read as %v, want clear", err)
			}
		})
	}
}

// A check that said nothing about custody is no answer to keep, so the next
// read asks again.
func TestAnUnreadableCustodyCheckIsAskedAgain(t *testing.T) {
	// Arrange
	runner := &finishedCheckRunner{progress: Progress{RunID: recoveryRun, RepoID: recoveryRepo, Status: "cancelled", Head: recoveryRecorded, SubmittedHead: recoveryPreserved}, err: context.DeadlineExceeded}
	r := newRecoveryReader(t, runner)
	r.Custody = NewCustodyCache()

	// Act
	first := r.CanSteer(context.Background(), "C:/project", "C:/work", recoveryBranch)
	second := r.CanSteer(context.Background(), "C:/project", "C:/work", recoveryBranch)

	// Assert
	if !errors.Is(first, ErrCustodyCheckUnreadable) || !errors.Is(second, ErrCustodyCheckUnreadable) {
		t.Fatalf("first=%v second=%v, want both unreadable", first, second)
	}
	if runner.checks != 2 {
		t.Fatalf("two reads started %d checks, want 2", runner.checks)
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

// StepDetails reads what each step of a run is doing, in order: when it
// started, its last activity and the agent working it, from no-mistakes'
// own columns, with nothing for a step that has not started.
func TestStepDetailsReadsWhatEachStepIsDoing(t *testing.T) {
	root := t.TempDir()
	database := filepath.Join(root, "state.sqlite")
	sql := `CREATE TABLE step_results(run_id TEXT,step_name TEXT,status TEXT,step_order INTEGER,started_at INTEGER,round_started_at INTEGER,last_activity_at INTEGER,last_activity TEXT,agent_pid INTEGER);
INSERT INTO step_results VALUES('run','test','running',2,1791297214,1791300000,1791325002,'go test ./...',34824),('run','review','completed',1,1791297171,NULL,1791297214,'status: completed',NULL),('run','lint','pending',3,NULL,NULL,NULL,NULL,NULL),('other','review','running',1,1,1,1,'x',1);`
	result, err := (execx.OSRunner{}).Run(context.Background(), execx.Request{Name: "sqlite3", Args: []string{database, sql}})
	if err != nil {
		t.Skipf("sqlite3 unavailable: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatal(string(result.Stderr))
	}

	steps, err := (Reader{Root: root, Commands: execx.OSRunner{}}).StepDetails(context.Background(), "run")

	want := []StepDetail{
		{Name: "review", Status: "completed", StartedAt: 1791297171, LastActivityAt: 1791297214, LastActivity: "status: completed"},
		{Name: "test", Status: "running", StartedAt: 1791297214, RoundStartedAt: 1791300000, LastActivityAt: 1791325002, LastActivity: "go test ./...", AgentPID: 34824},
		{Name: "lint", Status: "pending"},
	}
	if err != nil || fmt.Sprint(steps) != fmt.Sprint(want) {
		t.Fatalf("StepDetails = %+v, %v; want %+v", steps, err, want)
	}
}

// stepDatabase is a no-mistakes state database in a new root holding sql,
// or skips the test where sqlite3 is not installed.
func stepDatabase(t *testing.T, sql string) Reader {
	t.Helper()
	root := t.TempDir()
	result, err := (execx.OSRunner{}).Run(context.Background(), execx.Request{Name: "sqlite3", Args: []string{filepath.Join(root, "state.sqlite"), sql}})
	if err != nil {
		t.Skipf("sqlite3 unavailable: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatal(string(result.Stderr))
	}
	return Reader{Root: root, Commands: execx.OSRunner{}}
}

// A goblin can name the gate run it waits on, which may be on any branch of
// any repository.
func TestRunReadsTheRunWithItsID(t *testing.T) {
	// Arrange
	reader := stepDatabase(t, `CREATE TABLE repos(id TEXT,working_path TEXT);
CREATE TABLE runs(id TEXT,repo_id TEXT,branch TEXT,created_at INTEGER,status TEXT,head_sha TEXT,submitted_head_sha TEXT,review_approved_head_sha TEXT,last_pushed_sha TEXT,pr_url TEXT,pr_state TEXT,ci_ready_at INTEGER,terminal_head_verified_at INTEGER,custody_returned_at INTEGER);
INSERT INTO repos VALUES('repo','C:\project');
INSERT INTO runs(id,repo_id,branch,created_at,status,head_sha) VALUES('named','repo','split',1,'running','named'),('newer','repo','split',2,'completed','newer');`)

	// Act
	named, err := reader.Run(context.Background(), "named")
	_, missingErr := reader.Run(context.Background(), "gone")

	// Assert
	if err != nil || named.RunID != "named" || named.Status != "running" || named.Head != "named" {
		t.Fatalf("Run = %+v, %v; want the named run", named, err)
	}
	if !errors.Is(missingErr, ErrNoProgress) {
		t.Fatalf("Run of an unknown ID = %v, want ErrNoProgress", missingErr)
	}
}

// How long a step usually runs is read per round, since a fix round runs
// the step again, from the time nine in ten completed rounds took.
func TestUsualStepTimesIsWhatNineInTenRoundsFinishWithin(t *testing.T) {
	// Arrange: ten completed test rounds of 1 to 10 minutes, one of them
	// a fix round started an hour after its step, a review round started
	// before the round column existed, and steps that did not complete.
	sql := `CREATE TABLE step_results(run_id TEXT,step_name TEXT,status TEXT,started_at INTEGER,round_started_at INTEGER,completed_at INTEGER);`
	for minutes := 1; minutes <= 10; minutes++ {
		sql += fmt.Sprintf("INSERT INTO step_results VALUES('r%d','test','completed',1000,1000,%d);", minutes, 1000+minutes*60)
	}
	sql += `INSERT INTO step_results VALUES('fix','test','completed',1000,4600,4660);
INSERT INTO step_results VALUES('old','review','completed',1000,NULL,1300);
INSERT INTO step_results VALUES('now','test','running',1000,1000,NULL),('cut','lint','failed',1000,1000,99000),('later','lint','pending',NULL,NULL,NULL);`
	reader := stepDatabase(t, sql)

	// Act
	usual, err := reader.UsualStepTimes(context.Background())

	// Assert
	want := map[string]time.Duration{"test": 9 * time.Minute, "review": 5 * time.Minute}
	if err != nil || fmt.Sprint(usual) != fmt.Sprint(want) {
		t.Fatalf("UsualStepTimes = %v, %v; want %v", usual, err, want)
	}
}

// A custody check that did not finish says nothing about custody, so it is
// never reported as a hold: a probe that gave up before no-mistakes answered
// must not read as the gate owning the branch, while a real refusal still is.
func TestACustodyCheckThatCouldNotFinishIsNotAHold(t *testing.T) {
	progress := Progress{RunID: recoveryRun, RepoID: recoveryRepo, Status: "cancelled", Head: recoveryRecorded, SubmittedHead: recoveryRecorded}
	for _, test := range []struct {
		name  string
		check execx.Result
		err   error
		want  string
	}{
		{"the probe expired", execx.Result{}, context.DeadlineExceeded, custodyCheckTimedOut},
		{"the probe was cancelled", execx.Result{}, context.Canceled, custodyCheckCancelled},
		{"the program would not start", execx.Result{}, errors.New("exec: \"no-mistakes\": executable file not found"), custodyCheckLaunchFailed},
		{"the check ended on another exit", execx.Result{ExitCode: 3, Stdout: []byte("branch_sync:\n  state: user_owned\n")}, nil, custodyCheckExit},
		{"the check printed no state", execx.Result{ExitCode: 1, Stdout: []byte("not: [valid")}, nil, custodyCheckUnreadableOutput},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			runner := &finishedCheckRunner{progress: progress, result: test.check, err: test.err}

			// Act
			err := newRecoveryReader(t, runner).CanSteer(context.Background(), "C:/project", "C:/work", recoveryBranch)

			// Assert: it is unreadable custody with its own class, and never a hold.
			if !errors.Is(err, ErrCustodyCheckUnreadable) {
				t.Fatalf("the check read as %v, want it named unreadable", err)
			}
			if errors.Is(err, ErrCustodyHeld) || errors.Is(err, ErrCustodyNotReturned) {
				t.Fatalf("a check that said nothing about custody was reported as a hold: %v", err)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("the class reads %q, want %q", err.Error(), test.want)
			}
			if test.err != nil && strings.Contains(err.Error(), test.err.Error()) {
				t.Fatalf("the error repeats the command's own text: %q", err.Error())
			}
		})
	}

	// A blocked check still prints its state, which is a real hold.
	t.Run("a blocked check is a hold", func(t *testing.T) {
		// Arrange
		runner := &finishedCheckRunner{progress: progress, result: execx.Result{ExitCode: 1, Stdout: []byte("branch_sync:\n  state: pipeline_owned\n  safety: blocked_pipeline_owned_recoverable\n")}}

		// Act
		err := newRecoveryReader(t, runner).CanSteer(context.Background(), "C:/project", "C:/work", recoveryBranch)

		// Assert
		if !errors.Is(err, ErrCustodyHeld) && !errors.Is(err, ErrCustodyNotReturned) {
			t.Fatalf("a blocked check read as %v, want a hold", err)
		}
	})
}

// finishedCheckRunner answers the run query, then whatever the custody check
// was given, so each way a check can end is exercised.
type finishedCheckRunner struct {
	progress Progress
	result   execx.Result
	err      error
	checks   int
}

func (r *finishedCheckRunner) Run(ctx context.Context, req execx.Request) (execx.Result, error) {
	if req.Name == "sqlite3" {
		if strings.Contains(req.Args[len(req.Args)-1], "FROM step_results") {
			return execx.Result{Stdout: []byte("[]")}, nil
		}
		data, _ := json.Marshal([]Progress{r.progress})
		return execx.Result{Stdout: data}, nil
	}
	if req.Name == "no-mistakes" {
		r.checks++
		return r.result, r.err
	}
	return execx.Result{}, fmt.Errorf("unexpected program %s", req.Name)
}
