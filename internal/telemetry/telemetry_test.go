package telemetry

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

type fakeRunner struct {
	result   execx.Result
	err      error
	requests []execx.Request
}

func (r *fakeRunner) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	r.requests = append(r.requests, request)
	return r.result, r.err
}

func fakeDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.sqlite")
	if err := os.WriteFile(path, []byte("not a real database; the runner is faked"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSpeedTableParsesMeasuredRows(t *testing.T) {
	runner := &fakeRunner{result: execx.Result{Stdout: []byte(`[{"agent":"kimi","step_name":"review","n":12,"avg_min":3.456,"max_min":9.1},{"agent":"pi","step_name":"test","n":4,"avg_min":1.5,"max_min":2.0}]`)}}
	querier := Querier{Commands: runner, DBPath: fakeDB(t)}

	rows, note := querier.SpeedTable(context.Background())
	if note != "" {
		t.Fatalf("note = %q, want a parsed table", note)
	}
	if len(rows) != 2 {
		t.Fatalf("len(rows) = %d, want 2", len(rows))
	}
	if rows[0] != (SpeedRow{Agent: "kimi", Step: "review", Count: 12, AvgMin: 3.456, MaxMin: 9.1}) {
		t.Errorf("rows[0] = %+v, want the decoded kimi row", rows[0])
	}
	if len(runner.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(runner.requests))
	}
	args := runner.requests[0].Args
	if runner.requests[0].Name != "sqlite3" || args[0] != "-readonly" || args[1] != "-json" || args[2] != querier.DBPath {
		t.Errorf("sqlite3 invocation = %s %s, want read-only JSON query against the database", runner.requests[0].Name, strings.Join(args, " "))
	}
}

func TestSpeedTableSkipsWithoutDatabase(t *testing.T) {
	runner := &fakeRunner{}
	querier := Querier{Commands: runner, DBPath: filepath.Join(t.TempDir(), "absent.sqlite")}

	rows, note := querier.SpeedTable(context.Background())
	if rows != nil || !strings.Contains(note, "no telemetry database") {
		t.Errorf("rows = %v note = %q, want a skip note", rows, note)
	}
	if len(runner.requests) != 0 {
		t.Error("sqlite3 invoked without a database file")
	}
}

func TestSpeedTableSkipsWithoutSQLite3(t *testing.T) {
	runner := &fakeRunner{err: errors.New("executable file not found")}
	querier := Querier{Commands: runner, DBPath: fakeDB(t)}

	rows, note := querier.SpeedTable(context.Background())
	if rows != nil || !strings.Contains(note, "sqlite3 is not available") {
		t.Errorf("rows = %v note = %q, want a skip note", rows, note)
	}
}

func TestSpeedTableSkipsLockedDatabase(t *testing.T) {
	runner := &fakeRunner{result: execx.Result{Stderr: []byte("Error: database is locked"), ExitCode: 1}}
	querier := Querier{Commands: runner, DBPath: fakeDB(t)}

	rows, note := querier.SpeedTable(context.Background())
	if rows != nil || !strings.Contains(note, "database is locked") {
		t.Errorf("rows = %v note = %q, want the lock surfaced in a skip note", rows, note)
	}
}

func TestSpeedTableSkipsEmptyTelemetry(t *testing.T) {
	for _, stdout := range []string{"", "   \n", "[]"} {
		runner := &fakeRunner{result: execx.Result{Stdout: []byte(stdout)}}
		querier := Querier{Commands: runner, DBPath: fakeDB(t)}

		rows, note := querier.SpeedTable(context.Background())
		if rows != nil || !strings.Contains(note, "no recorded invocations") {
			t.Errorf("stdout=%q: rows = %v note = %q, want a skip note", stdout, rows, note)
		}
	}
}

func TestSpeedTableNormalizesPrefixedAgentIdentities(t *testing.T) {
	sqlite3, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	path := filepath.Join(t.TempDir(), "state.sqlite")
	setup := `CREATE TABLE agent_invocations(agent TEXT, step_name TEXT, duration_ms INTEGER, model TEXT, purpose TEXT, exit_status TEXT);` +
		`INSERT INTO agent_invocations VALUES('acp:kimi','review',60000,'k2','review','ok'),('kimi','review',120000,'k2','review','ok');`
	if out, err := exec.Command(sqlite3, path, setup).CombinedOutput(); err != nil {
		t.Fatalf("create fixture database: %v\n%s", err, out)
	}

	rows, note := (Querier{Commands: execx.OSRunner{}, DBPath: path}).SpeedTable(context.Background())
	if note != "" {
		t.Fatalf("note = %q, want a parsed table", note)
	}
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1 normalized row: %+v", len(rows), rows)
	}
	if rows[0].Agent != "kimi" || rows[0].Step != "review" || rows[0].Count != 2 {
		t.Errorf("rows[0] = %+v, want kimi/review merged across prefixed identities", rows[0])
	}
	if rows[0].AvgMin != 1.5 || rows[0].MaxMin != 2.0 {
		t.Errorf("rows[0] = %+v, want merged avg 1.5 and max 2.0 minutes", rows[0])
	}
}

func TestFailureLatencyIsNotSuccessfulSpeed(t *testing.T) {
	sqlite, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	path := filepath.Join(t.TempDir(), "state.sqlite")
	sql := `CREATE TABLE agent_invocations(agent TEXT, step_name TEXT, duration_ms INTEGER, model TEXT, purpose TEXT, exit_status TEXT);
INSERT INTO agent_invocations VALUES
('claude','review',600000,'opus','review','ok'),
('claude','review',100,'opus','review','error'),
('claude','review',200,'opus','review','cancelled'),
('claude','review',60000,'opus','review-fix','ok'),
('claude','review',120000,'other','review','ok'),
('codex','review',10,NULL,'review','error');`
	if out, err := exec.Command(sqlite, path, sql).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, out)
	}
	q := Querier{Commands: execx.OSRunner{}, DBPath: path}
	rows, note := q.SpeedTable(context.Background())
	if note != "" || len(rows) != 6 {
		t.Fatalf("model/role/outcome groups = %+v, %s", rows, note)
	}
	for _, row := range rows {
		if row.Agent == "claude" && row.Model == "opus" && row.Role == "review" && row.Outcome == "ok" {
			if row.Count != 1 || row.AvgMin != 10 {
				t.Fatalf("failure latency contaminated successful timing: %+v", row)
			}
		}
	}
	hint := FormatHint("codex", rows)
	if !strings.Contains(hint, "0 successful, 1 failed, 0 cancelled") || !strings.Contains(hint, "implementation unmeasured") {
		t.Fatalf("failed-only hint: %s", hint)
	}
	hint = FormatHint("claude", rows)
	if !strings.Contains(hint, "3 successful, 1 failed, 1 cancelled") {
		t.Fatalf("mixed outcome hint: %s", hint)
	}
}

type blockingRunner struct{}

func (blockingRunner) Run(ctx context.Context, _ execx.Request) (execx.Result, error) {
	<-ctx.Done()
	return execx.Result{}, ctx.Err()
}

func TestSpeedTableTimesOutWhenSQLite3Hangs(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	querier := Querier{Commands: blockingRunner{}, DBPath: fakeDB(t)}

	rows, note := querier.SpeedTable(ctx)
	if rows != nil || !strings.Contains(note, "timed out") {
		t.Fatalf("rows = %v note = %q, want a timeout skip note", rows, note)
	}
}
