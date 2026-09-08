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
)

func TestGateReadsLatestBranchRoundFromSQLite(t *testing.T) {
	sqlite, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	dir := t.TempDir()
	sql := `CREATE TABLE repos(id TEXT,working_path TEXT,default_branch TEXT);
CREATE TABLE runs(id TEXT,repo_id TEXT,branch TEXT,created_at INTEGER,status TEXT);
CREATE TABLE step_results(id TEXT,run_id TEXT,step_name TEXT,status TEXT,auto_fix_limit INTEGER,findings_json TEXT,step_order INTEGER);
CREATE TABLE step_rounds(step_result_id TEXT,round INTEGER,selection_source TEXT);
INSERT INTO repos VALUES('repo','C:\project','main');
INSERT INTO runs VALUES('old','repo','feat',1,'completed'),('current','repo','feat',2,'running'),('other','repo','other',3,'running');
INSERT INTO step_results VALUES('oldstep','old','review','completed',10,'{}',1),('step','current','review','awaiting_approval',0,'{"findings":[]}',1),('otherstep','other','review','awaiting_approval',0,'{}',1);
INSERT INTO step_rounds VALUES('step',1,'user'),('step',2,'user'),('step',3,NULL);`
	if out, err := exec.Command(sqlite, filepath.Join(dir, "state.sqlite"), sql).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %s %v", out, err)
	}
	reader := Reader{Root: dir, Commands: execx.OSRunner{}}
	gate, err := reader.Gate(context.Background(), "C:/project", "feat")
	if err != nil || gate.RunID != "current" || gate.Round != 3 || gate.Selected != "" || gate.AutoFixLimit == nil || *gate.AutoFixLimit != 0 {
		t.Fatalf("gate: %+v %v", gate, err)
	}
	if _, err := reader.Gate(context.Background(), "C:/project", "absent"); err == nil {
		t.Fatal("absent branch inherited another gate")
	}
	if out, err := exec.Command(sqlite, filepath.Join(dir, "state.sqlite"), `UPDATE step_rounds SET selection_source='user' WHERE step_result_id='step' AND round=3`).CombinedOutput(); err != nil {
		t.Fatalf("selection: %s %v", out, err)
	}
	gate, err = reader.Gate(context.Background(), "C:/project", "feat")
	if err != nil || gate.Selected != "user" {
		t.Fatalf("selection lost: %+v %v", gate, err)
	}
}

func TestRepoPolicyCannotRaiseCapsOrChangeReviewer(t *testing.T) {
	p := testPolicy(t)
	for _, source := range []string{"auto_fix: {review: 10}", "auto_fix: {test: 2}", "agent: [claude, pi]", "agent: codex", "auto_fix: {review: 0, review: 10}", "agent: [claude]\n---\nagent: [pi]"} {
		if err := CheckRepoConfig([]byte(source), p); err == nil {
			t.Errorf("unsafe override accepted: %s", source)
		}
	}
	for _, source := range []string{"agent: [claude]\nauto_fix: {review: 0, test: 1}", "disable_project_settings: true"} {
		if err := CheckRepoConfig([]byte(source), p); err != nil {
			t.Errorf("valid override refused: %s: %v", source, err)
		}
	}
	if err := CheckRepoConfig([]byte("auto_fix: {babysit: 10}"), p); err == nil {
		t.Error("legacy CI alias bypassed automatic budget")
	}
}

func TestCheckStartRefusesOnlyANonTerminalPreviousRun(t *testing.T) {
	sqlite, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	for _, c := range []struct {
		status     string
		unresolved bool
	}{
		{"completed", false},
		{"failed", false},
		{"cancelled", false},
		{"running", true},
		{"awaiting_approval", true},
	} {
		t.Run(c.status, func(t *testing.T) {
			dir := t.TempDir()
			sql := `CREATE TABLE repos(id TEXT,working_path TEXT,default_branch TEXT);
CREATE TABLE runs(id TEXT,repo_id TEXT,branch TEXT,created_at INTEGER,status TEXT);
INSERT INTO repos VALUES('repo','C:\project','main');
INSERT INTO runs VALUES('previous','repo','feat',1,'` + c.status + `');`
			if out, err := exec.Command(sqlite, filepath.Join(dir, "state.sqlite"), sql).CombinedOutput(); err != nil {
				t.Fatalf("fixture: %s %v", out, err)
			}
			reader := Reader{Root: dir, Commands: execx.OSRunner{}}
			err := reader.CheckStart(context.Background(), "C:/project", t.TempDir(), "feat", testPolicy(t))
			if errors.Is(err, ErrUnresolved) != c.unresolved {
				t.Fatalf("status %q: %v", c.status, err)
			}
		})
	}
}

// The native engine persists a disabled auto-fix budget as SQL NULL, never 0,
// so the decode-and-respond path has to accept a null limit or no real review
// gate can ever be answered.
func TestResponseAcceptsTheNullReviewLimitTheEngineActuallyWrites(t *testing.T) {
	sqlite, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	dir := t.TempDir()
	sql := `CREATE TABLE repos(id TEXT,working_path TEXT,default_branch TEXT);
CREATE TABLE runs(id TEXT,repo_id TEXT,branch TEXT,created_at INTEGER,status TEXT);
CREATE TABLE step_results(id TEXT,run_id TEXT,step_name TEXT,status TEXT,auto_fix_limit INTEGER,findings_json TEXT,step_order INTEGER);
CREATE TABLE step_rounds(step_result_id TEXT,round INTEGER,selection_source TEXT);
INSERT INTO repos VALUES('repo','C:\project','main');
INSERT INTO runs VALUES('disabled','repo','feat/disabled',1,'running'),('automatic','repo','feat/automatic',2,'running');
INSERT INTO step_results VALUES('disabledstep','disabled','review','awaiting_approval',NULL,'{"findings":[{"id":"bug","action":"auto-fix"}]}',1),('automaticstep','automatic','review','awaiting_approval',10,'{"findings":[{"id":"bug","action":"auto-fix"}]}',1);
INSERT INTO step_rounds VALUES('disabledstep',1,NULL),('automaticstep',1,NULL);`
	if out, err := exec.Command(sqlite, filepath.Join(dir, "state.sqlite"), sql).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %s %v", out, err)
	}
	selection, err := testPolicy(t).Select("ordinary")
	if err != nil {
		t.Fatal(err)
	}
	reader := Reader{Root: dir, Commands: execx.OSRunner{}}
	gate, err := reader.Gate(context.Background(), "C:/project", "feat/disabled")
	if err != nil || gate.AutoFixLimit != nil {
		t.Fatalf("null limit did not decode as nil: %+v %v", gate, err)
	}
	args, err := ResponseArgs(selection, gate, Response{Action: "fix", Findings: "bug"})
	if err != nil {
		t.Fatalf("null review limit refused: %v", err)
	}
	want := "axi respond --step review --action fix --findings bug"
	if strings.Join(args, " ") != want {
		t.Fatalf("args: %q", strings.Join(args, " "))
	}
	// A live automatic budget still means the step is repairing itself, which
	// is the case the guard exists to refuse.
	automatic, err := reader.Gate(context.Background(), "C:/project", "feat/automatic")
	if err != nil || automatic.AutoFixLimit == nil || *automatic.AutoFixLimit != 10 {
		t.Fatalf("automatic gate: %+v %v", automatic, err)
	}
	if _, err := ResponseArgs(selection, automatic, Response{Action: "fix", Findings: "bug"}); err == nil {
		t.Fatal("automatic review budget accepted")
	}
}

// This repository's own committed .no-mistakes.yaml is what bounds the initial
// rollout gate, and CheckStart reads it through checkRepoConfig. Editing either
// checked-in artifact out of agreement would only surface as a refused gate
// run, so assert the real consumer accepts the real pair.
func TestCommittedRepoConfigSatisfiesTheCheckedInPolicy(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", ".no-mistakes.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckRepoConfig(data, testPolicy(t)); err != nil {
		t.Fatalf("committed gate config conflicts with config/pipeline.json: %v", err)
	}
}
