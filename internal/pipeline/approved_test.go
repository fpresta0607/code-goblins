package pipeline

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// no-mistakes records approve, skip and abort alike as a user_declined
// round; approve completes the step, skip marks it skipped and abort leaves
// it unfinished. Only the first two let a gate's report through.
func TestApprovedGateSummariesAreTheGateParksAPersonLetThrough(t *testing.T) {
	sqlite, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	// Arrange
	dir := t.TempDir()
	sql := `CREATE TABLE step_results(id TEXT,run_id TEXT,step_name TEXT,status TEXT);
CREATE TABLE step_rounds(id TEXT,step_result_id TEXT,round INTEGER,selection_source TEXT,findings_json TEXT,created_at INTEGER);
INSERT INTO step_results VALUES
('approved','r1','gate.lint.tests-kept','completed'),
('skipped','r2','gate.lint.tests-kept','skipped'),
('aborted','r3','gate.lint.tests-kept','awaiting_approval'),
('fixed','r4','gate.lint.tests-kept','completed'),
('passed','r5','gate.lint.tests-kept','completed'),
('review','r6','review','completed'),
('malformed','r7','gate.lint.tests-kept','completed');
INSERT INTO step_rounds VALUES
('1','approved',1,'user_declined','{"summary":"approved first\nbelow"}',1),
('2','skipped',1,'user_declined','{"summary":"skipped"}',2),
('3','aborted',1,'user_declined','{"summary":"aborted"}',3),
('4','fixed',1,'user','{"summary":"fixed"}',4),
('5','passed',1,NULL,'{"summary":"passed"}',5),
('6','review',1,'user_declined','{"summary":"review"}',6),
('7','malformed',1,'user_declined','not json',7);`
	if out, err := exec.Command(sqlite, filepath.Join(dir, "state.sqlite"), sql).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %s %v", out, err)
	}
	reader := Reader{Root: dir, Commands: execx.OSRunner{}}

	// Act
	summaries, err := reader.ApprovedGateSummaries(context.Background())

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(summaries, "|"); got != "skipped|approved first\nbelow" {
		t.Fatalf("summaries = %q, want the skipped and the approved park, newest first", summaries)
	}
}

func TestApprovedGateSummariesFailWithoutADatabase(t *testing.T) {
	reader := Reader{Root: t.TempDir(), Commands: execx.OSRunner{}}

	if _, err := reader.ApprovedGateSummaries(context.Background()); err == nil {
		t.Fatal("no database read as no approvals; want an error, so the check says it read none")
	}
}
