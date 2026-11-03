package pipeline

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/gatedb"
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

func TestResponseSelectsRepairWithInformationalIDCollision(t *testing.T) {
	selection, err := testPolicy(t).Select("ordinary")
	if err != nil {
		t.Fatal(err)
	}
	const findings = `{"findings":[{"id":"test-1","severity":"info","action":"no-op","description":"Earlier validation completed"},{"id":"test-1","severity":"error","action":"ask-user","description":"Current validation needs a correction"}],"summary":"Live validation needs repair","tested":["focused regression"],"testing_summary":"One scenario failed","scenarios":[{"name":"selected repair","status":"fail","evidence":"The current response was refused"}],"verdict":"no-go"}`
	gate := Gate{RunID: "run", StepID: "step", Step: "test", Status: "awaiting_approval", Round: 2, Findings: findings}
	response := Response{Action: "fix", Findings: "test-1", Instructions: "Repair the current validation failure"}

	args, err := ResponseArgs(selection, gate, response)

	want := []string{"axi", "respond", "--step", "test", "--action", "fix", "--findings", "test-1", "--instructions", response.Instructions}
	if err != nil || !slices.Equal(args, want) {
		t.Fatalf("got %q, %v; want %q", args, err, want)
	}
}

func TestResponseDistinguishesInformationalAndActionableIDCollisions(t *testing.T) {
	selection, err := testPolicy(t).Select("ordinary")
	if err != nil {
		t.Fatal(err)
	}
	limit := 1
	for _, test := range []struct {
		name        string
		firstAction string
		lastAction  string
		response    Response
		want        []string
		wantErr     bool
	}{
		{name: "informational then automatic repair", firstAction: "no-op", lastAction: "auto-fix", response: Response{Action: "fix", Findings: "test-1"}, want: []string{"axi", "respond", "--step", "test", "--action", "fix", "--findings", "test-1"}},
		{name: "actionable then informational", firstAction: "ask-user", lastAction: "no-op", response: Response{Action: "fix", Findings: "test-1"}, want: []string{"axi", "respond", "--step", "test", "--action", "fix", "--findings", "test-1"}},
		{name: "informational then budgeted empty action", firstAction: "no-op", response: Response{Action: "fix", Findings: "test-1"}, want: []string{"axi", "respond", "--step", "test", "--action", "fix", "--findings", "test-1"}},
		{name: "budgeted empty action then informational", lastAction: "no-op", response: Response{Action: "fix", Findings: "test-1"}, want: []string{"axi", "respond", "--step", "test", "--action", "fix", "--findings", "test-1"}},
		{name: "two informational findings are clean", firstAction: "no-op", lastAction: "no-op", response: Response{Action: "approve"}, want: []string{"axi", "respond", "--step", "test", "--action", "approve"}},
		{name: "explicit acceptance still names the open finding", firstAction: "no-op", lastAction: "ask-user", response: Response{Action: "approve", Accept: "test-1"}, want: []string{"axi", "respond", "--step", "test", "--action", "approve"}},
		{name: "no implicit acceptance", firstAction: "no-op", lastAction: "ask-user", response: Response{Action: "approve"}, wantErr: true},
		{name: "no acceptance of an empty action", firstAction: "no-op", response: Response{Action: "approve", Accept: "test-1"}, wantErr: true},
		{name: "no repair of informational findings", firstAction: "no-op", lastAction: "no-op", response: Response{Action: "fix", Findings: "test-1"}, wantErr: true},
		{name: "no repeated selection", firstAction: "no-op", lastAction: "auto-fix", response: Response{Action: "fix", Findings: "test-1,test-1"}, wantErr: true},
		{name: "no missing selection", firstAction: "no-op", lastAction: "auto-fix", response: Response{Action: "fix", Findings: "absent"}, wantErr: true},
		{name: "two automatic repairs are ambiguous", firstAction: "auto-fix", lastAction: "auto-fix", response: Response{Action: "fix", Findings: "test-1"}, wantErr: true},
		{name: "two user decisions are ambiguous", firstAction: "ask-user", lastAction: "ask-user", response: Response{Action: "fix", Findings: "test-1"}, wantErr: true},
		{name: "different actions are ambiguous", firstAction: "auto-fix", lastAction: "ask-user", response: Response{Action: "fix", Findings: "test-1"}, wantErr: true},
		{name: "empty and explicit actions are ambiguous", lastAction: "auto-fix", response: Response{Action: "fix", Findings: "test-1"}, wantErr: true},
		{name: "two empty actions are ambiguous", response: Response{Action: "fix", Findings: "test-1"}, wantErr: true},
		{name: "unknown action after informational is refused", firstAction: "no-op", lastAction: "unknown", response: Response{Action: "fix", Findings: "test-1"}, wantErr: true},
		{name: "informational after unknown action is refused", firstAction: "unknown", lastAction: "no-op", response: Response{Action: "approve"}, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			gate := Gate{RunID: "run", StepID: "step", Step: "test", Status: "awaiting_approval", Round: 2, AutoFixLimit: &limit, Findings: `{"findings":[{"id":"test-1","action":"` + test.firstAction + `"},{"id":"test-1","action":"` + test.lastAction + `"}]}`}

			args, err := ResponseArgs(selection, gate, test.response)

			if test.wantErr {
				if err == nil || len(args) != 0 {
					t.Fatalf("got %q, %v; want refusal without native arguments", args, err)
				}
			} else if err != nil || !slices.Equal(args, test.want) {
				t.Fatalf("got %q, %v; want %q", args, err, test.want)
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
		func(g *Gate) { g.RunID = "" }, func(g *Gate) { g.StepID = "" },
		func(g *Gate) { g.Step = "" },
		func(g *Gate) { g.AutoFixLimit = &ten },
		func(g *Gate) { g.Status = "fixing" }, func(g *Gate) { g.Selected = "user" },
		func(g *Gate) { g.Round = 0 }, func(g *Gate) { g.Findings = `{}` },
		func(g *Gate) { g.Findings = `{"findings":[{"id":"","action":"no-op"}]}` },
		func(g *Gate) { g.Findings = `{"findings":[{"id":"","action":"auto-fix"}]}` },
		func(g *Gate) { g.Findings = `{"findings":[{"id":"bug","action":"new-unknown-action"}]}` },
	} {
		gate := valid
		mutate(&gate)
		if _, err := ResponseArgs(selection, gate, Response{Action: "fix", Findings: "bug"}); err == nil {
			t.Fatalf("unsafe gate accepted: %+v", gate)
		}
	}
	for _, mutate := range []func(*Selection){
		func(s *Selection) { s.Class = "unknown" },
		func(s *Selection) { s.Hash = "invalid" },
		func(s *Selection) { s.ReviewCycles++ },
	} {
		invalid := selection
		mutate(&invalid)
		if args, err := ResponseArgs(invalid, valid, Response{Action: "fix", Findings: "bug"}); err == nil || len(args) != 0 {
			t.Fatalf("unsafe selection accepted: %q, %v", args, err)
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
		{"sqlite3 never answers", context.Background(), sqliteRun{takes: time.Duration(math.MaxInt64)}, "state query timed out: the gate's database gave no answer within 1m0s"},
		{"its caller stopped the read", stopped, sqliteRun{}, "state query stopped: context canceled"},
		{"sqlite3 is not installed", context.Background(), sqliteRun{err: &exec.Error{Name: "sqlite3", Err: exec.ErrNotFound}}, `could not run sqlite3: exec: "sqlite3": executable file not found`},
		{"the database is locked", context.Background(), sqliteRun{result: execx.Result{ExitCode: 1, Stderr: []byte("Error: database is locked\n")}}, "the gate's database refused it: Error: database is locked"},
		{"the database is not the gate's", context.Background(), sqliteRun{result: execx.Result{ExitCode: 1, Stderr: []byte("Parse error in 4th command line argument: no such table: runs\n")}}, "the gate's database refused it: Parse error in 4th command line argument: no such table: runs"},
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

// The fleet's runner answers a read of the gate's database in process, so a
// read starts no sqlite3: the board started 1,259 of them in 95 minutes on
// 2026-11-03, one for each read (#1610). Here no sqlite3 can be found, and
// any program the read starts fails it.
func TestAStateReadThroughTheFleetsRunnerStartsNoProgram(t *testing.T) {
	// Arrange
	t.Setenv("PATH", t.TempDir())
	root := t.TempDir()
	writer, err := sql.Open("sqlite", filepath.Join(root, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if _, err := writer.Exec(`PRAGMA journal_mode=WAL;
CREATE TABLE repos(id TEXT,working_path TEXT);
CREATE TABLE runs(id TEXT,repo_id TEXT,branch TEXT,created_at INTEGER);
INSERT INTO repos VALUES('repo','C:\work\project');
INSERT INTO runs VALUES('run','repo','fix/connections',1);`); err != nil {
		t.Fatal(err)
	}
	started := &startRecorder{}
	reader := Reader{Root: root, Commands: gatedb.Runner{Next: started}}

	// Act
	branch, err := reader.RunBranch(context.Background(), "C:/work/project", "run")

	// Assert
	if err != nil || branch != "fix/connections" || len(started.requests) != 0 {
		t.Fatalf("got %q, %v, started %v; want the branch read with no program started", branch, err, started.requests)
	}
}

// startRecorder records each program it is asked to start and starts none.
type startRecorder struct{ requests []execx.Request }

func (s *startRecorder) Run(_ context.Context, req execx.Request) (execx.Result, error) {
	s.requests = append(s.requests, req)
	return execx.Result{}, errors.New("no program may start")
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

// An approved Test exception keeps the operator's reason: it reaches the
// native --reason, and every use the native engine would drop or store as none
// is refused here instead, so a Test exception never reads as one nobody
// explained (#1422).
func TestApprovedTestExceptionCarriesTheOperatorReason(t *testing.T) {
	selection, err := testPolicy(t).Select("ordinary")
	if err != nil {
		t.Fatal(err)
	}
	open := Gate{RunID: "run", StepID: "step", Step: "test", Status: "awaiting_approval", Round: 1, Findings: `{"findings":[{"id":"test-2","action":"ask-user"}]}`}
	quiet := Gate{RunID: "run", StepID: "step", Step: "test", Status: "awaiting_approval", Round: 1, Findings: `{"findings":[]}`}
	review := Gate{RunID: "run", StepID: "step", Step: "review", Status: "awaiting_approval", Round: 1, Findings: `{"findings":[{"id":"bug","action":"auto-fix"}]}`}
	const reason = "the only failure was the sandbox's missing compiler, read in the step log"
	for _, c := range []struct {
		name string
		gate Gate
		// response is the answer the CFO gave.
		response Response
		// want is the exact argv handed to the native engine.
		want string
		// refusal is the rule a refused answer must name; unresolved means it
		// stays a decision instead.
		refusal    string
		unresolved bool
	}{
		{name: "a Test exception whose open finding the CFO accepted", gate: open, response: Response{Action: "approve", Accept: "test-2", Reason: reason}, want: "axi respond --step test --action approve --reason " + reason},
		{name: "a Test gate with nothing open", gate: quiet, response: Response{Action: "approve", Reason: reason}, want: "axi respond --step test --action approve --reason " + reason},
		{name: "a reason on another step", gate: review, response: Response{Action: "approve", Reason: reason}, refusal: "applies only to an approve of the test step, and this gate is review"},
		{name: "a reason with fix", gate: review, response: Response{Action: "fix", Findings: "bug", Reason: reason}, refusal: "never with fix"},
		{name: "a reason of nothing but space", gate: quiet, response: Response{Action: "approve", Reason: "   "}, refusal: "must say why the exception is approved"},
		{name: "a reason of more than one line", gate: open, response: Response{Action: "approve", Accept: "test-2", Reason: "the compiler\nwas missing"}, refusal: "must be one line of plain text"},
		{name: "a reason where an open finding is left out", gate: open, response: Response{Action: "approve", Reason: reason}, unresolved: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Act
			args, err := ResponseArgs(selection, c.gate, c.response)

			// Assert
			switch {
			case c.unresolved:
				// A reason is never consent: the finding still needs the CFO's
				// explicit --accept.
				if !errors.Is(err, ErrUnresolved) || len(args) != 0 {
					t.Fatalf("got %v %q, want it unresolved", err, args)
				}
			case c.refusal != "":
				if err == nil || errors.Is(err, ErrUnresolved) || !strings.Contains(err.Error(), c.refusal) || len(args) != 0 {
					t.Fatalf("got %v %q, want a refusal naming %q", err, args, c.refusal)
				}
			default:
				if err != nil || strings.Join(args, " ") != c.want {
					t.Fatalf("got %v %q, want %q", err, args, c.want)
				}
			}
		})
	}
}

func TestRequiredEnvironmentRefusalRequiresRetryInsteadOfApproval(t *testing.T) {
	selection, err := testPolicy(t).Select("high-risk")
	if err != nil {
		t.Fatal(err)
	}
	// The isolated real-daemon fixture's native engine recorded these
	// findings for its refused runs, kept verbatim but for machine paths.
	recorded := func(name string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	for _, report := range []struct {
		name, findings, finding string
	}{
		{"recorded native refusal", recorded("native-environment-refusal.json"), "test-command-no-turn"},
		{"recorded native refusal through verify", recorded("native-environment-refusal-through-verify.json"), "test-command-no-turn"},
		{"before admission", `{"findings":[{"id":"test-command-no-turn","action":"ask-user","description":"The wrapper reported: cfo gate turn: the command did not start: required test environment: .venv is missing. Respond with fix to run it again."}]}`, "test-command-no-turn"},
		{"after admission", `{"findings":[{"id":"test-command-no-turn","action":"ask-user","description":"The wrapper reported: cfo gate turn: the command did not start: required test environment changed while waiting; verify the project binding and retry."}]}`, "test-command-no-turn"},
		{"base attribution", `{"findings":[{"id":"test-1","action":"ask-user","description":"configured test command exited with code 7"}],"summary":"Base attribution unavailable (test command was given no turn on the base checkout: cfo gate turn: the command did not start: required test environment: .venv is missing); every failure is attributed to this change."}`, "test-1"},
	} {
		t.Run(report.name, func(t *testing.T) {
			gate := Gate{RunID: "run", StepID: "step", Step: "test", Status: "awaiting_approval", Round: 1, Findings: report.findings}
			for _, response := range []Response{
				{Action: "approve"},
				{Action: "approve", Accept: report.finding},
				{Action: "approve", Reason: "the environment is repaired"},
				{Action: "approve", Accept: report.finding, Reason: "the environment is repaired"},
			} {
				args, err := ResponseArgs(selection, gate, response)
				if err == nil || len(args) != 0 || !strings.Contains(err.Error(), "--action fix --findings "+report.finding) {
					t.Fatalf("response=%+v args=%v err=%v; want required-binding refusal left unresolved with a concrete retry", response, args, err)
				}
			}

			args, err := ResponseArgs(selection, gate, Response{Action: "fix", Findings: report.finding})
			if err != nil || strings.Join(args, " ") != "axi respond --step test --action fix --findings "+report.finding {
				t.Fatalf("retry args=%v err=%v", args, err)
			}
		})
	}
	for _, report := range []struct {
		name, findings string
	}{
		{"unrelated no-turn refusal", `{"findings":[{"id":"test-command-no-turn","action":"ask-user","description":"The test command ` + "`echo required test environment`" + ` was given no turn. The wrapper reported: cfo gate turn: this run takes no turn: it took no turn within its wait of 1m0s."}]}`},
		{"unrelated base refusal", `{"findings":[{"id":"test-command-no-turn","action":"ask-user","description":"The wrapper reported: cfo gate turn: this run takes no turn: it took no turn within its wait of 1m0s."}],"summary":"Base attribution unavailable (test command was given no turn on the base checkout: cfo gate turn: this run takes no turn: queue timeout); every failure is attributed to this change.\nTest output mentions required test environment."}`},
	} {
		t.Run(report.name, func(t *testing.T) {
			gate := Gate{RunID: "run", StepID: "step", Step: "test", Status: "awaiting_approval", Round: 1, Findings: report.findings}
			args, err := ResponseArgs(selection, gate, Response{Action: "approve", Accept: "test-command-no-turn", Reason: "the operator accepts the queue timeout"})
			if err != nil || len(args) == 0 {
				t.Fatalf("args=%v err=%v; want the existing unrelated approval available", args, err)
			}
		})
	}
}
