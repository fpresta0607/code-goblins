package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/measure"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// A done report records what each role of the run spent since the task's
// spawn: the lead from the goblin's own conversation, its implement worker
// from its sub-agent's, and the review and verify roles from the gate's
// runs of the task's branch, with the escalations its status log recorded.
// What it spent before its spawn is no part of it, and the next report
// starts where this one ended.
func TestMeasureRunRecordsEachRoleOfTheRunSinceItsLastReport(t *testing.T) {
	// Arrange
	h := testHome(t)
	if err := os.MkdirAll(h.State, 0o700); err != nil {
		t.Fatal(err)
	}
	userHome := t.TempDir()
	t.Setenv("USERPROFILE", userHome)
	t.Setenv("HOME", userHome)
	gateHome := t.TempDir()
	t.Setenv("NM_HOME", gateHome)
	project := t.TempDir()
	worktree := t.TempDir()
	gitIn(t, worktree, "init", "-q", "-b", "feat/measured")
	gitIn(t, worktree, "-c", "user.name=t", "-c", "user.email=t@example.test", "commit", "-q", "--allow-empty", "-m", "task branch")
	spawned := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	minute := func(minutes int) time.Time { return spawned.Add(time.Duration(minutes) * time.Minute) }
	meta := state.TaskMeta{ID: "g1", Project: project, Worktree: worktree, Harness: "claude", Model: "opus", Effort: "xhigh", ImplementModel: "sonnet", ImplementEffort: "high", SpawnGen: fmt.Sprintf("s%d", spawned.UnixNano())}
	conversation := measuredConversation(t, h, userHome, meta, minute(1))
	writeFixture(t, conversation+".jsonl", claudeAnswerLine("before", "claude-opus-5-5", minute(-10), 999, 999)+claudeAnswerLine("lead-1", "claude-opus-5-5", minute(10), 300, 30)+claudeAnswerLine("lead-2", "claude-opus-5-5", minute(20), 200, 20))
	writeFixture(t, filepath.Join(conversation, "subagents", "agent-implement.jsonl"), claudeAnswerLine("implement", "claude-sonnet-5-5", minute(15), 1000, 100))
	writeFixture(t, filepath.Join(conversation, "subagents", "agent-implement.meta.json"), `{"agentType":"ceo-implement"}`)
	database, err := sql.Open("sqlite", filepath.Join(gateHome, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.Exec(`
CREATE TABLE repos(id TEXT, working_path TEXT);
CREATE TABLE runs(id TEXT, repo_id TEXT, branch TEXT, created_at INTEGER);
CREATE TABLE agent_invocations(id TEXT, run_id TEXT, purpose TEXT, agent TEXT, model TEXT, started_at INTEGER, duration_ms INTEGER, input_tokens INTEGER, output_tokens INTEGER, cache_read_tokens INTEGER, finding_count INTEGER);
CREATE TABLE step_results(id TEXT, run_id TEXT, step_name TEXT, step_order INTEGER, status TEXT);
INSERT INTO agent_invocations VALUES ('e1', 'earlier', 'review', 'claude', 'claude-opus-5-5', 0, 9000, 9, 9, 9, 9), ('r1', 'gate-run', 'review', 'claude', 'claude-opus-5-5', 0, 60000, 500, 50, 400, 2), ('t1', 'gate-run', 'test', 'codex', 'gpt-6.1-sol', 0, 30000, 80, 8, 0, 0);
INSERT INTO step_results VALUES ('s1', 'gate-run', 'test', 4, 'completed');
`)
	if err != nil {
		t.Fatal(err)
	}
	// The driver binds each statement's arguments from the first, so each
	// statement that takes some is its own.
	if _, err := database.Exec(`INSERT INTO repos VALUES ('repo', ?)`, project); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO runs VALUES ('earlier', 'repo', 'feat/measured', ?), ('gate-run', 'repo', 'feat/measured', ?)`, minute(-30).Unix(), minute(25).Unix()); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	escalation := "implement sonnet high -> opus after run gate-run's failed test step"
	writeFixture(t, state.StatusPath(h.State, meta.ID), minute(-5).Format(time.RFC3339)+" role-escalated: implement before the spawn\n"+minute(26).Format(time.RFC3339)+" role-escalated: "+escalation+"\n")

	// Act
	first, err := measureRun(context.Background(), h, meta, "done", "https://github.com/o/r/pull/1")
	if err != nil {
		t.Fatal(err)
	}
	second, secondErr := measureRun(context.Background(), h, meta, "done", "https://github.com/o/r/pull/1")

	// Assert
	if secondErr != nil {
		t.Fatal(secondErr)
	}
	wantRoles := []measure.Role{
		{Role: measure.RoleLead, Harness: "claude", Model: "claude-opus-5-5", Effort: "xhigh", Calls: 1, Usage: measure.Usage{InputTokens: 500, OutputTokens: 50}, WallSeconds: 600},
		{Role: measure.RoleImplement, Harness: "claude", Model: "claude-sonnet-5-5", Effort: "high", Calls: 1, Usage: measure.Usage{InputTokens: 1000, OutputTokens: 100}},
		{Role: measure.RoleReview, Harness: "claude", Model: "claude-opus-5-5", Calls: 1, Usage: measure.Usage{InputTokens: 500, OutputTokens: 50, CacheReadTokens: 400}, WallSeconds: 60, Findings: 2},
		{Role: measure.RoleVerify, Harness: "codex", Model: "gpt-6.1-sol", Calls: 1, Usage: measure.Usage{InputTokens: 80, OutputTokens: 8}, WallSeconds: 30, Passed: 1},
	}
	if !first.From.Equal(spawned) || !reflect.DeepEqual(first.Roles, wantRoles) || !reflect.DeepEqual(first.Runs, []string{"gate-run"}) || !reflect.DeepEqual(first.Escalations, []string{escalation}) || len(first.Unmeasured) != 0 {
		t.Errorf("first record = %+v\nwant from %v, roles %+v, gate run gate-run, escalation %q and nothing unmeasured", first, spawned, wantRoles, escalation)
	}
	if !second.From.Equal(first.To) || len(second.Roles) != 0 || len(second.Runs) != 0 {
		t.Errorf("second record = %+v, want it from %v with nothing spent", second, first.To)
	}
	records, err := measure.Read(h.State)
	if err != nil || len(records) != 2 {
		t.Errorf("recorded %d runs (%v), want both", len(records), err)
	}
}

// A goblin relaunched in its run holds a conversation from its relaunch, so
// what one before it spent is named unmeasured rather than passed over; a
// run that starts at the goblin's own launch names nothing.
func TestMeasureRunNamesTheSpendBeforeARelaunchUnmeasured(t *testing.T) {
	launched := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	relaunched := launched.Add(time.Hour)
	generation := func(at time.Time) string { return fmt.Sprintf("s%d", at.UnixNano()) }
	cases := []struct {
		name    string
		meta    state.TaskMeta
		isNamed bool
	}{
		{name: "relaunched in the run", meta: state.TaskMeta{ID: "g1", FirstGen: generation(launched), SpawnGen: generation(relaunched)}, isNamed: true},
		{name: "on its first launch", meta: state.TaskMeta{ID: "g1", FirstGen: generation(launched), SpawnGen: generation(launched)}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			h := testHome(t)
			if err := os.MkdirAll(h.State, 0o700); err != nil {
				t.Fatal(err)
			}

			// Act
			record, err := measureRun(context.Background(), h, testCase.meta, "done", "https://github.com/o/r/pull/1")

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			want := "lead and workers before " + relaunched.Format(time.RFC3339) + ": the task was relaunched then, and only the conversation it holds now is read"
			if isNamed := slices.Contains(record.Unmeasured, want); isNamed != testCase.isNamed || !record.From.Equal(launched) {
				t.Errorf("record from %v named unmeasured %q; want it from %v, naming the relaunch %t", record.From, record.Unmeasured, launched, testCase.isNamed)
			}
		})
	}
}

// A task's id is used again once its task is cleaned up, and the home's
// measurements outlive the task, so a run starts at the later of the id's
// last recorded report and this task's own spawn: a run an earlier task of
// the id recorded is no part of it, and names no relaunch.
func TestMeasureRunStartsNoEarlierThanTheTasksOwnSpawn(t *testing.T) {
	launched := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	generation := fmt.Sprintf("s%d", launched.UnixNano())
	cases := []struct {
		name     string
		reported time.Time
		wantFrom time.Time
	}{
		{name: "an earlier task of its id reported before its spawn", reported: launched.AddDate(0, 0, -8), wantFrom: launched},
		{name: "it reported since its spawn", reported: launched.Add(30 * time.Minute), wantFrom: launched.Add(30 * time.Minute)},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			h := testHome(t)
			if err := os.MkdirAll(h.State, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := measure.Append(h.State, measure.Record{Task: "g1", Outcome: "done", From: testCase.reported.Add(-time.Hour), To: testCase.reported}); err != nil {
				t.Fatal(err)
			}
			meta := state.TaskMeta{ID: "g1", Worktree: t.TempDir(), FirstGen: generation, SpawnGen: generation}

			// Act
			record, err := measureRun(context.Background(), h, meta, "done", "https://github.com/o/r/pull/1")

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			isRelaunchNamed := slices.ContainsFunc(record.Unmeasured, func(unmeasured string) bool { return strings.HasPrefix(unmeasured, "lead and workers before ") })
			if !record.From.Equal(testCase.wantFrom) || isRelaunchNamed {
				t.Errorf("record from %v named unmeasured %q; want it from %v, naming no relaunch", record.From, record.Unmeasured, testCase.wantFrom)
			}
		})
	}
}

// A worker that cannot be read is named unmeasured on its own: the lead's
// conversation was read, and what it spent is recorded.
func TestMeasureRunNamesAnUnreadableWorkerApartFromTheLead(t *testing.T) {
	// Arrange
	h := testHome(t)
	if err := os.MkdirAll(h.State, 0o700); err != nil {
		t.Fatal(err)
	}
	userHome := t.TempDir()
	t.Setenv("USERPROFILE", userHome)
	t.Setenv("HOME", userHome)
	spawned := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	meta := state.TaskMeta{ID: "g1", Worktree: t.TempDir(), Harness: "claude", SpawnGen: fmt.Sprintf("s%d", spawned.UnixNano())}
	conversation := measuredConversation(t, h, userHome, meta, spawned.Add(time.Minute))
	writeFixture(t, conversation+".jsonl", claudeAnswerLine("lead", "claude-opus-5-5", spawned.Add(10*time.Minute), 300, 30))
	writeFixture(t, filepath.Join(conversation, "subagents", "agent-a.jsonl"), claudeAnswerLine("worker", "claude-sonnet-5-5", spawned.Add(15*time.Minute), 1000, 100))
	writeFixture(t, filepath.Join(conversation, "subagents", "agent-a.meta.json"), `{"agentType":`)

	// Act
	record, err := measureRun(context.Background(), h, meta, "done", "")

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	wantRoles := []measure.Role{{Role: measure.RoleLead, Harness: "claude", Model: "claude-opus-5-5", Calls: 1, Usage: measure.Usage{InputTokens: 300, OutputTokens: 30}}}
	isWorkersNamed := slices.ContainsFunc(record.Unmeasured, func(unmeasured string) bool { return strings.HasPrefix(unmeasured, "workers: ") })
	isLeadNamed := slices.ContainsFunc(record.Unmeasured, func(unmeasured string) bool { return strings.HasPrefix(unmeasured, "lead and workers") })
	if !reflect.DeepEqual(record.Roles, wantRoles) || !isWorkersNamed || isLeadNamed {
		t.Errorf("record roles %+v, unmeasured %q; want roles %+v with the workers alone named unmeasured", record.Roles, record.Unmeasured, wantRoles)
	}
}

func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// measuredConversation records the session measured-session as meta's
// conversation, proved at provedAt, and is where Claude Code keeps it under
// userHome for meta's worktree, without its .jsonl, which is also the folder
// of its sub-agents.
func measuredConversation(t *testing.T, h home.Home, userHome string, meta state.TaskMeta, provedAt time.Time) string {
	t.Helper()
	if err := state.WriteConversation(h.State, state.Conversation{ID: meta.ID, Generation: meta.SpawnGen, Harness: meta.Harness, Session: "measured-session", ProvedAt: provedAt}); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(userHome, ".claude", "projects", claudeProjectFolder.ReplaceAllString(meta.Worktree, "-"), "measured-session")
}

// claudeAnswerLine is a Claude Code transcript's line for one answer.
func claudeAnswerLine(id, model string, at time.Time, input, output int) string {
	return fmt.Sprintf(`{"type":"assistant","timestamp":%q,"message":{"id":%q,"model":%q,"usage":{"input_tokens":%d,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":%d}}}`+"\n", at.Format(time.RFC3339Nano), id, model, input, output)
}

// A done or failed report measures its run once, when it is new, and says
// what it recorded: the CEO's task's when it wakes the CEO, and a helper's
// when it reaches the helper's parent, or the CEO when the parent's terminal
// refuses it. A report made again measures nothing twice, and a measurement
// that fails is said without failing the report.
func TestNotifyMeasuresEachNewDoneOrFailedReportOnce(t *testing.T) {
	recorded := "Recorded this run's role measurements (lead, review) over 2 gate runs.\n"
	cases := []struct {
		name       string
		reports    [][]string
		sendErr    error
		measureErr error
		wantCalls  []string
		wantStdout string
		wantStderr string
	}{
		{
			name:       "a done reported twice for one head",
			reports:    [][]string{{"g1", "--done", "--pr", "https://github.com/o/r/pull/1"}, {"g1", "--done", "--pr", "https://github.com/o/r/pull/1 (ready to land)"}},
			wantCalls:  []string{"g1 done https://github.com/o/r/pull/1"},
			wantStdout: recorded + "Unmeasured: gate: unreadable.\n",
		},
		{
			name:       "a failed report",
			reports:    [][]string{{"g1", "--failed", "the build cannot start"}},
			wantCalls:  []string{"g1 failed "},
			wantStdout: recorded,
		},
		{
			name:       "a measurement that fails",
			reports:    [][]string{{"g1", "--done", "--pr", "https://github.com/o/r/pull/1"}},
			measureErr: errors.New("state folder is read-only"),
			wantCalls:  []string{"g1 done https://github.com/o/r/pull/1"},
			wantStderr: "goblins notify: this run's role measurements could not be recorded: state folder is read-only\n",
		},
		{
			name:    "a working report",
			reports: [][]string{{"g1", "--working", "writing the tests"}},
		},
		{
			name:       "a helper's done that reaches its parent",
			reports:    [][]string{{"g1-h1", "--done"}},
			wantCalls:  []string{"g1-h1 done "},
			wantStdout: recorded + "Unmeasured: gate: unreadable.\n",
		},
		{
			name:       "a helper's failed that reaches its parent",
			reports:    [][]string{{"g1-h1", "--failed", "the build is broken"}},
			wantCalls:  []string{"g1-h1 failed "},
			wantStdout: recorded,
		},
		{
			name:       "a helper's done its parent's terminal refuses, so the CEO is told",
			reports:    [][]string{{"g1-h1", "--done"}},
			sendErr:    errors.New("the pane is gone"),
			wantCalls:  []string{"g1-h1 done "},
			wantStdout: recorded + "Unmeasured: gate: unreadable.\n",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			_, runtime, _ := helperHome(t, testCase.sendErr)
			runtime.github = proofRunner{stdout: strings.Repeat("a", 40) + "\n"}
			var calls []string
			runtime.measureRun = func(_ context.Context, _ home.Home, meta state.TaskMeta, outcome, pr string) (measure.Record, error) {
				calls = append(calls, meta.ID+" "+outcome+" "+pr)
				record := measure.Record{Task: meta.ID, Runs: []string{"one", "two"}, Roles: []measure.Role{{Role: measure.RoleLead}, {Role: measure.RoleReview}, {Role: measure.RoleReview, Harness: "codex"}}}
				if outcome == "done" {
					record.Unmeasured = []string{"gate: unreadable"}
				}
				return record, testCase.measureErr
			}
			var stdout, stderr bytes.Buffer

			// Act
			for _, report := range testCase.reports {
				if exit := runNotify(report, &stdout, &stderr, runtime); exit != 0 {
					t.Fatalf("exit=%d stderr=%q", exit, stderr.String())
				}
			}

			// Assert
			if !reflect.DeepEqual(calls, testCase.wantCalls) {
				t.Errorf("measured %q, want %q", calls, testCase.wantCalls)
			}
			if said := measurementLines(stdout.String()); said != testCase.wantStdout {
				t.Errorf("notify said %q of its measurement, want %q (stdout %q)", said, testCase.wantStdout, stdout.String())
			}
			if said := measurementLines(stderr.String()); said != testCase.wantStderr {
				t.Errorf("notify warned %q of its measurement, want %q (stderr %q)", said, testCase.wantStderr, stderr.String())
			}
		})
	}
}

// measurementLines is what output says of a run's role measurements.
func measurementLines(output string) string {
	var said strings.Builder
	for line := range strings.Lines(output) {
		if strings.Contains(line, "role measurements") || strings.HasPrefix(line, "Unmeasured: ") {
			said.WriteString(line)
		}
	}
	return said.String()
}

// goblins doctor sums every recorded run's roles into one row each, by role,
// harness, model, effort and agent, with the calls the gate tallied no
// tokens for named, in place of the old line that called implementation
// unmeasured; a home that recorded none says so, and one whose record
// cannot be read says that.
func TestDoctorReportsWhatEachRoleSpentOverEveryRecordedRun(t *testing.T) {
	cases := []struct {
		name    string
		records string
		want    []string
	}{
		{
			name: "no run recorded",
			want: []string{"role measurements: none yet; each done or failed report records its run's"},
		},
		{
			name:    "a record that cannot be read",
			records: `{"task":` + "\n",
			want:    []string{"role measurements: unreadable (measure: line 1 of role-measurements.jsonl: unexpected end of JSON input)"},
		},
		{
			name: "two runs",
			records: `{"task":"g1","roles":[{"role":"lead","harness":"claude","model":"claude-opus-5-5","effort":"xhigh","calls":1,"input_tokens":1000000,"output_tokens":100000,"wall_seconds":3600},{"role":"fix","harness":"claude","model":"claude-opus-5-5","agent":"review-fix","calls":2,"untallied":1,"wall_seconds":90}]}` + "\n" +
				`{"task":"g2","roles":[{"role":"lead","harness":"claude","model":"claude-opus-5-5","effort":"xhigh","calls":1,"input_tokens":500000,"output_tokens":20000,"wall_seconds":1800},{"role":"verify","harness":"codex","model":"gpt-6.1-sol","calls":1,"verification_passed":1}]}` + "\n",
			want: []string{
				"role measurements: 2 runs, from each role's transcripts and the gate's calls; tokens in millions",
				"role harness model effort runs calls input output hours findings passed failed",
				"lead claude claude-opus-5-5 xhigh 2 2 1.5 0.1 1.5 0 0 0",
				"verify codex gpt-6.1-sol - 1 1 0.0 0.0 0.0 0 1 0",
				"fix review-fix claude claude-opus-5-5 - 1 2 0.0 0.0 0.0 0 0 0 (1 calls untallied)",
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			root := t.TempDir()
			t.Setenv("GOBLINS_HOME", root)
			if testCase.records != "" {
				writeFixture(t, filepath.Join(root, "state", measure.FileName), testCase.records)
			}
			var stdout, stderr bytes.Buffer

			// Act
			run([]string{"doctor"}, &stdout, &stderr)

			// Assert
			var got []string
			isTable := false
			for line := range strings.Lines(stdout.String()) {
				line = strings.TrimRight(line, "\r\n")
				switch {
				case strings.HasPrefix(line, "role measurements: "):
					isTable = true
					got = append(got, line)
				case isTable && strings.HasPrefix(line, "  "):
					got = append(got, strings.Join(strings.Fields(line), " "))
				default:
					isTable = false
				}
			}
			if !reflect.DeepEqual(got, testCase.want) || strings.Contains(stdout.String(), "telemetry: implementation unmeasured") {
				t.Errorf("doctor said\n%s\nwant\n%s\nand no telemetry line (stdout %s)", strings.Join(got, "\n"), strings.Join(testCase.want, "\n"), stdout.String())
			}
		})
	}
}

// goblins notify, as it runs for real, records a failed report's run as one
// line of the home's role measurements.
func TestNotifyRecordsTheRunInTheHomesRoleMeasurements(t *testing.T) {
	// Arrange
	root := t.TempDir()
	t.Setenv("GOBLINS_HOME", root)
	userHome := t.TempDir()
	t.Setenv("USERPROFILE", userHome)
	t.Setenv("HOME", userHome)
	stateDir := filepath.Join(root, "state")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteTaskMeta(stateDir, state.TaskMeta{ID: "g1", Worktree: t.TempDir(), SpawnGen: "s1"}); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer

	// Act
	exit := run([]string{"notify", "g1", "--failed", "the build cannot start"}, &stdout, &stderr)

	// Assert
	records, err := measure.Read(stateDir)
	if exit != 0 || err != nil || len(records) != 1 || records[0].Task != "g1" || records[0].Outcome != "failed" {
		t.Errorf("exit %d recorded %+v (%v), want one failed run of g1\nstdout %s\nstderr %s", exit, records, err, stdout.String(), stderr.String())
	}
}
