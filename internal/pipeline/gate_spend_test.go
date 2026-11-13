package pipeline

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/gatedb"
)

// What a run of a task's branch spent is read from the gate's own record of
// each agent call, by purpose, harness and model, for the runs created in
// the window alone: a run before it, one of another branch and one of
// another repository are no part of it. A run's Test step is read with the
// harness and model of its last test agent, and a run with none has none.
func TestGateSpendReadsTheCallsOfTheBranchsRunsInTheWindow(t *testing.T) {
	// Arrange
	root := t.TempDir()
	project := filepath.Join(root, "project")
	writer, err := sql.Open("sqlite", filepath.Join(root, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = writer.Exec(`
CREATE TABLE repos(id TEXT, working_path TEXT);
CREATE TABLE runs(id TEXT, repo_id TEXT, branch TEXT, created_at INTEGER);
CREATE TABLE agent_invocations(id TEXT, run_id TEXT, purpose TEXT, agent TEXT, model TEXT, started_at INTEGER, duration_ms INTEGER, input_tokens INTEGER, output_tokens INTEGER, cache_read_tokens INTEGER, finding_count INTEGER);
CREATE TABLE step_results(id TEXT, run_id TEXT, step_name TEXT, step_order INTEGER, status TEXT);
INSERT INTO repos VALUES ('repo', ?), ('other-repo', ?);
INSERT INTO runs VALUES ('before', 'repo', 'feat/x', 100), ('first', 'repo', 'feat/x', 200), ('second', 'repo', 'feat/x', 300), ('other-branch', 'repo', 'feat/y', 250), ('other-repo', 'other-repo', 'feat/x', 250), ('untested', 'repo', 'feat/x', 400);
INSERT INTO agent_invocations VALUES
 ('b1', 'before', 'review', 'claude', 'claude-opus-5-5', 101, 9000, 9, 9, 9, 9),
 ('f1', 'first', 'review', 'claude', 'claude-opus-5-5', 201, 60000, 1000, 100, 800, 3),
 ('f2', 'first', 'review', 'claude', 'claude-opus-5-5', 205, 30000, 500, 50, 400, 1),
 ('f3', 'first', 'review-fix', 'claude', 'claude-opus-5-5', 203, 45000, NULL, NULL, NULL, NULL),
 ('f4', 'first', 'test', 'acp:kimi', 'kimi-k3', 206, 20000, 300, 30, 0, 0),
 ('s1', 'second', 'test', 'codex', 'gpt-6.1-sol', 301, 10000, 200, 20, 100, 0),
 ('s2', 'second', 'test', 'claude', '', 302, 5000, 100, 10, 50, 0),
 ('y1', 'other-branch', 'review', 'claude', 'claude-opus-5-5', 251, 1, 1, 1, 1, 1),
 ('o1', 'other-repo', 'review', 'claude', 'claude-opus-5-5', 251, 1, 1, 1, 1, 1);
INSERT INTO step_results VALUES ('t1', 'first', 'test', 4, 'failed'), ('t2', 'second', 'test', 4, 'completed');
`, project, filepath.Join(root, "other"))
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	reader := Reader{Root: root, Commands: gatedb.Runner{Next: execx.OSRunner{}}}

	// Act
	calls, tests, err := reader.GateSpend(context.Background(), project, "feat/x", time.Unix(100, 0), time.Unix(400, 0))

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	wantCalls := []GateCalls{
		{Run: "first", Purpose: "review", Agent: "claude", Model: "claude-opus-5-5", Calls: 2, DurationMS: 90000, InputTokens: 1500, OutputTokens: 150, CacheReadTokens: 1200, Findings: 4},
		{Run: "first", Purpose: "review-fix", Agent: "claude", Model: "claude-opus-5-5", Calls: 1, DurationMS: 45000, Untallied: 1},
		{Run: "first", Purpose: "test", Agent: "kimi", Model: "kimi-k3", Calls: 1, DurationMS: 20000, InputTokens: 300, OutputTokens: 30},
		{Run: "second", Purpose: "test", Agent: "claude", Model: "", Calls: 1, DurationMS: 5000, InputTokens: 100, OutputTokens: 10, CacheReadTokens: 50},
		{Run: "second", Purpose: "test", Agent: "codex", Model: "gpt-6.1-sol", Calls: 1, DurationMS: 10000, InputTokens: 200, OutputTokens: 20, CacheReadTokens: 100},
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Errorf("calls =\n%+v\nwant\n%+v", calls, wantCalls)
	}
	wantTests := []GateTest{
		{Run: "first", Status: "failed", Agent: "kimi", Model: "kimi-k3"},
		{Run: "second", Status: "completed", Agent: "claude", Model: ""},
		{Run: "untested"},
	}
	if !reflect.DeepEqual(tests, wantTests) {
		t.Errorf("tests =\n%+v\nwant\n%+v", tests, wantTests)
	}
}
