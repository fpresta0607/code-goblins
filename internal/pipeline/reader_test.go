package pipeline

import (
	"context"
	"os/exec"
	"path/filepath"
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
