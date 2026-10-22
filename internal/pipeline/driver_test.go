package pipeline

import (
	"context"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

func TestReviewDecisionBudgetAndUnresolvedFindings(t *testing.T) {
	selection, err := testPolicy(t).Select("ordinary")
	if err != nil {
		t.Fatal(err)
	}
	gate := Gate{RunID: "run", StepID: "step", Step: "review", Status: "awaiting_approval", Round: 1, Findings: `{"findings":[{"id":"bug","action":"auto-fix"}]}`}
	args, err := ResponseArgs(selection, gate, Response{Action: "fix", Findings: "bug"})
	if err != nil || !strings.Contains(strings.Join(args, " "), "--step review") {
		t.Fatalf("first repair: %v %v", args, err)
	}
	gate.Round = 3
	if _, err := ResponseArgs(selection, gate, Response{Action: "fix", Findings: "bug"}); !errors.Is(err, ErrUnresolved) {
		t.Fatalf("exhausted: %v", err)
	}
	if _, err := ResponseArgs(selection, gate, Response{Action: "approve"}); !errors.Is(err, ErrUnresolved) {
		t.Fatalf("approve bypass: %v", err)
	}
	gate.Findings = `{"findings":[{"id":"note","action":"no-op"}]}`
	if _, err := ResponseArgs(selection, gate, Response{Action: "approve"}); err != nil {
		t.Fatalf("clean final review: %v", err)
	}
	gate.Findings = `{"findings":[{"id":"bug","action":"auto-fix"}]}`
	selection, err = testPolicy(t).Select("high-risk")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ResponseArgs(selection, gate, Response{Action: "fix", Findings: "bug"}); err != nil {
		t.Fatalf("third high-risk repair: %v", err)
	}
	gate.Round = 4
	if _, err := ResponseArgs(selection, gate, Response{Action: "fix", Findings: "bug"}); !errors.Is(err, ErrUnresolved) {
		t.Fatalf("high-risk exhausted: %v", err)
	}
}

func TestResponseAllowsSelectedEmptyActionRebaseFindings(t *testing.T) {
	selection, err := testPolicy(t).Select("ordinary")
	if err != nil {
		t.Fatal(err)
	}
	const findings = `{"findings":[{"id":"rebase-1","severity":"warning","file":"apps/api/src/drizzle-repositories.ts","action":"","description":"merge conflict rebasing onto origin/main"},{"id":"rebase-2","severity":"warning","file":"apps/api/src/repositories.ts","action":"","description":"merge conflict rebasing onto origin/main"},{"id":"rebase-3","severity":"warning","file":"apps/api/src/routes/me-stats.test.ts","action":"","description":"merge conflict rebasing onto origin/main"},{"id":"rebase-4","severity":"warning","file":"apps/api/src/routes/reports.test.ts","action":"","description":"merge conflict rebasing onto origin/main"},{"id":"rebase-5","severity":"warning","file":"apps/api/src/services/reports.test.ts","action":"","description":"merge conflict rebasing onto origin/main"}]}`
	limit := 1
	gate := Gate{RunID: "01M2DCND6SBH95TVRP0YX2P3Z4", StepID: "step", Step: "rebase", Status: "awaiting_approval", Round: 1, AutoFixLimit: &limit, Findings: findings}
	selected := "rebase-1,rebase-2,rebase-3,rebase-4,rebase-5"
	args, err := ResponseArgs(selection, gate, Response{Action: "fix", Findings: selected})
	if err != nil {
		t.Fatal(err)
	}
	want := "axi respond --step rebase --action fix --findings " + selected
	if strings.Join(args, " ") != want {
		t.Fatalf("args=%q, want %q", strings.Join(args, " "), want)
	}
	if _, err := ResponseArgs(selection, gate, Response{Action: "approve"}); !errors.Is(err, ErrUnresolved) {
		t.Fatalf("approve bypass: %v", err)
	}

	gate.Step = "review"
	gate.AutoFixLimit = nil
	if _, err := ResponseArgs(selection, gate, Response{Action: "fix", Findings: selected}); !errors.Is(err, ErrUnresolved) {
		t.Fatalf("review accepted empty actions: %v", err)
	}
}

func TestResponseUsesTheActiveGateBudgetForEmptyActions(t *testing.T) {
	selection, err := testPolicy(t).Select("ordinary")
	if err != nil {
		t.Fatal(err)
	}
	positive, zero := 3, 0
	findings := `{"findings":[{"id":"finding","action":""}]}`
	for _, test := range []struct {
		name    string
		step    string
		limit   *int
		wantErr bool
	}{
		{name: "document with budget", step: "document", limit: &positive},
		{name: "test without budget", step: "test", limit: &zero, wantErr: true},
		{name: "review without budget", step: "review", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			gate := Gate{RunID: "run", StepID: "step", Step: test.step, Status: "awaiting_approval", Round: 1, AutoFixLimit: test.limit, Findings: findings}
			_, err := ResponseArgs(selection, gate, Response{Action: "fix", Findings: "finding"})
			if test.wantErr && !errors.Is(err, ErrUnresolved) {
				t.Fatalf("ResponseArgs error=%v, want unresolved", err)
			}
			if !test.wantErr && err != nil {
				t.Fatalf("ResponseArgs: %v", err)
			}
		})
	}
}

func TestResponseRefusesUnsafeAndStaleDecisions(t *testing.T) {
	selection, err := testPolicy(t).Select("ordinary")
	if err != nil {
		t.Fatal(err)
	}
	ten := 10
	valid := Gate{RunID: "run", StepID: "step", Step: "review", Status: "awaiting_approval", Round: 1, Findings: `{"findings":[{"id":"bug","action":"auto-fix"}]}`}
	for _, mutate := range []func(*Gate){
		func(g *Gate) { g.AutoFixLimit = &ten },
		func(g *Gate) { g.Status = "fixing" }, func(g *Gate) { g.Selected = "user" },
		func(g *Gate) { g.Round = 0 }, func(g *Gate) { g.Findings = `{}` },
		func(g *Gate) { g.Findings = `{"findings":[{"id":"bug","action":"new-unknown-action"}]}` },
	} {
		gate := valid
		mutate(&gate)
		if _, err := ResponseArgs(selection, gate, Response{Action: "fix", Findings: "bug"}); err == nil {
			t.Fatalf("unsafe gate accepted: %+v", gate)
		}
	}
	for _, response := range []Response{{Action: "skip"}, {Action: "approve"}, {Action: "fix"}, {Action: "fix", Findings: "absent"}} {
		if _, err := ResponseArgs(selection, valid, response); err == nil {
			t.Fatalf("unsafe response: %+v", response)
		}
	}
}

func TestIdleLockIsReleasedOnUnreadableDatabase(t *testing.T) {
	// A missing database is not proof of idleness; no daemon or config is changed.
	r := Reader{Root: t.TempDir()}
	if release, err := r.Idle(context.Background()); err == nil {
		release()
		t.Fatal("missing database accepted as idle")
	}
}

// sqliteRun answers a state query as sqlite3 does through execx.OSRunner:
// with result and err once it has taken takes, and with its context's error
// when that context ends first. It counts the time rather than waiting it, so
// a read that takes minutes takes none in a test.
type sqliteRun struct {
	takes  time.Duration
	result execx.Result
	err    error
}

func (s sqliteRun) Run(ctx context.Context, _ execx.Request) (execx.Result, error) {
	if err := ctx.Err(); err != nil {
		return execx.Result{}, err
	}
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < s.takes {
		return execx.Result{}, context.DeadlineExceeded
	}
	return s.result, s.err
}

func stateRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "state.sqlite"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

// A read of the gate's database starts sqlite3, which a loaded machine can
// take tens of seconds to start: a 5 s bound on each read failed `cfo
// pipeline recover` twice on 2026-10-20 while sqlite3 was installed and the
// database readable, and the same read passed once load dropped (#1286).
func TestAStateReadSlowedByALoadedMachineIsAnswered(t *testing.T) {
	// Arrange
	answer := execx.Result{Stdout: []byte(`[{"branch":"fix/connections"}]`)}
	reader := Reader{Root: stateRoot(t), Commands: sqliteRun{takes: 30 * time.Second, result: answer}}

	// Act
	branch, err := reader.RunBranch(context.Background(), "C:/project", "run")

	// Assert
	if err != nil || branch != "fix/connections" {
		t.Fatalf("got %q, %v; want the slow read answered", branch, err)
	}
}

// Each way a read of the gate's database can end says what ended it, so a
// read the deadline ended never sends its reader to reinstall a sqlite3 that
// is there (#1286).
func TestAFailedStateReadNamesWhatEndedIt(t *testing.T) {
	stopped, stop := context.WithCancel(context.Background())
	stop()
	for _, c := range []struct {
		name string
		ctx  context.Context
		run  sqliteRun
		want string
	}{
		{"sqlite3 never answers", context.Background(), sqliteRun{takes: time.Duration(math.MaxInt64)}, "state query timed out: sqlite3 gave no answer within 1m0s"},
		{"its caller stopped the read", stopped, sqliteRun{}, "state query stopped: context canceled"},
		{"sqlite3 is not installed", context.Background(), sqliteRun{err: &exec.Error{Name: "sqlite3", Err: exec.ErrNotFound}}, `could not run sqlite3: exec: "sqlite3": executable file not found`},
		{"the database is locked", context.Background(), sqliteRun{result: execx.Result{ExitCode: 1, Stderr: []byte("Error: database is locked\n")}}, "sqlite3 exited with code 1: Error: database is locked"},
		{"the database is not the gate's", context.Background(), sqliteRun{result: execx.Result{ExitCode: 1, Stderr: []byte("Parse error in 4th command line argument: no such table: runs\n")}}, "sqlite3 exited with code 1: Parse error in 4th command line argument: no such table: runs"},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			reader := Reader{Root: stateRoot(t), Commands: c.run}

			// Act
			_, err := reader.RunBranch(c.ctx, "C:/project", "run")

			// Assert
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want an error naming %q", err, c.want)
			}
		})
	}
}

// The CFO takes a gate's open findings as they stand only with approve and by
// naming exactly the open ask-user and auto-fix findings; every other shape
// is refused for its own reason or stays unresolved.
func TestApproveAcceptsExactlyTheOpenFindings(t *testing.T) {
	selection, err := testPolicy(t).Select("ordinary")
	if err != nil {
		t.Fatal(err)
	}
	limit := 1
	review := Gate{RunID: "run", StepID: "step", Step: "review", Status: "awaiting_approval", Round: 3, Findings: `{"findings":[{"id":"ask","action":"ask-user"},{"id":"bug","action":"auto-fix"},{"id":"note","action":"no-op"}]}`}
	blank := Gate{RunID: "run", StepID: "step", Step: "rebase", Status: "awaiting_approval", Round: 1, AutoFixLimit: &limit, Findings: `{"findings":[{"id":"ask","action":"ask-user"},{"id":"conflict","action":""}]}`}
	blankOnly := Gate{RunID: "run", StepID: "step", Step: "rebase", Status: "awaiting_approval", Round: 1, AutoFixLimit: &limit, Findings: `{"findings":[{"id":"conflict","action":""}]}`}
	quiet := Gate{RunID: "run", StepID: "step", Step: "review", Status: "awaiting_approval", Round: 3, Findings: `{"findings":[{"id":"note","action":"no-op"}]}`}
	for _, c := range []struct {
		name     string
		gate     Gate
		response Response
		// refusal is the rule a refused answer must name; unresolved means it
		// stays a decision instead.
		refusal    string
		unresolved bool
	}{
		{"every open finding, in any order", review, Response{Action: "approve", Accept: "bug,ask"}, "", false},
		{"an open finding left out", review, Response{Action: "approve", Accept: "ask"}, "exactly the open ask-user and auto-fix findings: ask,bug", false},
		{"a no-op finding named", review, Response{Action: "approve", Accept: "ask,bug,note"}, "exactly the open ask-user and auto-fix findings: ask,bug", false},
		{"an absent finding named", review, Response{Action: "approve", Accept: "ask,bug,other"}, "exactly the open ask-user and auto-fix findings: ask,bug", false},
		{"a finding named twice", review, Response{Action: "approve", Accept: "ask,bug,bug"}, "exactly the open ask-user and auto-fix findings: ask,bug", false},
		{"accept with fix", review, Response{Action: "fix", Findings: "bug", Accept: "ask,bug"}, "never with fix", false},
		{"nothing open to accept", quiet, Response{Action: "approve", Accept: "note"}, "no open ask-user or auto-fix finding", false},
		{"approve without accept", review, Response{Action: "approve"}, "", true},
		{"a finding with no action still needs its fix", blank, Response{Action: "approve", Accept: "ask"}, "", true},
		{"only a finding with no action still needs its fix", blankOnly, Response{Action: "approve", Accept: "conflict"}, "", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			args, err := ResponseArgs(selection, c.gate, c.response)
			switch {
			case c.unresolved:
				if !errors.Is(err, ErrUnresolved) {
					t.Fatalf("got %v %q, want it unresolved", err, args)
				}
			case c.refusal != "":
				if err == nil || errors.Is(err, ErrUnresolved) || !strings.Contains(err.Error(), c.refusal) {
					t.Fatalf("got %v %q, want a refusal naming %q", err, args, c.refusal)
				}
			default:
				if err != nil || strings.Join(args, " ") != "axi respond --step review --action approve" {
					t.Fatalf("got %v %q, want the gate approved", err, args)
				}
			}
		})
	}
}
