package telemetry

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	runner := &fakeRunner{result: execx.Result{Stdout: []byte(`[]`)}}
	querier := Querier{Commands: runner, DBPath: fakeDB(t)}

	rows, note := querier.SpeedTable(context.Background())
	if rows != nil || !strings.Contains(note, "no recorded invocations") {
		t.Errorf("rows = %v note = %q, want a skip note", rows, note)
	}
}

func TestHarnessAverage(t *testing.T) {
	runner := &fakeRunner{result: execx.Result{Stdout: []byte(`[{"n":37,"avg_min":12.34}]`)}}
	querier := Querier{Commands: runner, DBPath: fakeDB(t)}

	avgMin, count, ok := querier.HarnessAverage(context.Background(), "kimi")
	if !ok || avgMin != 12.34 || count != 37 {
		t.Errorf("HarnessAverage = %v, %d, %v; want 12.34, 37, true", avgMin, count, ok)
	}
	if !strings.Contains(runner.requests[0].Args[3], "agent = 'kimi'") || !strings.Contains(runner.requests[0].Args[3], "LIKE '%:kimi'") {
		t.Errorf("query = %q, want exact and prefixed agent filters", runner.requests[0].Args[3])
	}
}

func TestHarnessAverageUnavailableWithoutSamples(t *testing.T) {
	for _, test := range []struct {
		name   string
		stdout string
	}{
		{name: "no invocations", stdout: `[{"n":0,"avg_min":null}]`},
		{name: "undecodable", stdout: `{`},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &fakeRunner{result: execx.Result{Stdout: []byte(test.stdout)}}
			querier := Querier{Commands: runner, DBPath: fakeDB(t)}
			if _, _, ok := querier.HarnessAverage(context.Background(), "codex"); ok {
				t.Error("HarnessAverage ok = true, want false")
			}
		})
	}
}

func TestHarnessAverageSkipsWhenTelemetryIsGone(t *testing.T) {
	runner := &fakeRunner{err: errors.New("executable file not found")}
	querier := Querier{Commands: runner, DBPath: fakeDB(t)}
	if _, _, ok := querier.HarnessAverage(context.Background(), "kimi"); ok {
		t.Error("HarnessAverage ok = true without sqlite3, want false")
	}
	if _, _, ok := (Querier{Commands: runner}).HarnessAverage(context.Background(), "kimi"); ok {
		t.Error("HarnessAverage ok = true without a database path, want false")
	}
}
