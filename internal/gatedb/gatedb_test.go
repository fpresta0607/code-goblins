package gatedb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// refusingRunner is a Next that fails the test on every request it is given,
// and records it.
type refusingRunner struct{ requests []execx.Request }

func (r *refusingRunner) Run(_ context.Context, req execx.Request) (execx.Result, error) {
	r.requests = append(r.requests, req)
	return execx.Result{}, errors.New("no program may start")
}

// gateDatabase writes a database in write-ahead mode, as no-mistakes keeps
// its own, in a folder whose name holds a space and a '#', and returns its
// path and the open writer, which holds it as no-mistakes does.
func gateDatabase(t *testing.T, statements string) (string, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "no mistakes #1", "state.sqlite")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	writer, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { writer.Close() })
	if _, err := writer.Exec("PRAGMA journal_mode=WAL; " + statements); err != nil {
		t.Fatal(err)
	}
	return path, writer
}

func read(t *testing.T, path, statement string) (execx.Result, *refusingRunner) {
	t.Helper()
	next := &refusingRunner{}
	result, err := (Runner{Next: next}).Run(context.Background(), execx.Request{Name: "sqlite3", Args: []string{"-readonly", "-json", path, statement}})
	if err != nil {
		t.Fatal(err)
	}
	return result, next
}

func TestRunnerAnswersAReadInProcessAsSqlite3PrintsIt(t *testing.T) {
	path, _ := gateDatabase(t, `CREATE TABLE runs(id TEXT, attempts INTEGER, share REAL, note TEXT, raw BLOB);
INSERT INTO runs VALUES('run-1', 3, 1.5, 'said "hi"', x'414243'), ('run-2', 9007199254740993, NULL, NULL, NULL);`)

	result, next := read(t, path, "SELECT id, attempts, share, note, raw FROM runs ORDER BY id")

	if len(next.requests) != 0 {
		t.Fatalf("the read started %v", next.requests)
	}
	if result.ExitCode != 0 || len(result.Stderr) != 0 {
		t.Fatalf("result = %+v", result)
	}
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(result.Stdout, &rows); err != nil {
		t.Fatalf("%s: %v", result.Stdout, err)
	}
	want := []map[string]json.RawMessage{
		{"id": json.RawMessage(`"run-1"`), "attempts": json.RawMessage(`3`), "share": json.RawMessage(`1.5`), "note": json.RawMessage(`"said \"hi\""`), "raw": json.RawMessage(`"ABC"`)},
		{"id": json.RawMessage(`"run-2"`), "attempts": json.RawMessage(`9007199254740993`), "share": json.RawMessage(`null`), "note": json.RawMessage(`null`), "raw": json.RawMessage(`null`)},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("rows = %s", result.Stdout)
	}
}

func TestRunnerPrintsNothingForNoRows(t *testing.T) {
	path, _ := gateDatabase(t, `CREATE TABLE runs(id TEXT);`)

	result, _ := read(t, path, "SELECT id FROM runs")

	if result.ExitCode != 0 || len(result.Stdout) != 0 {
		t.Fatalf("result = %+v", result)
	}
}

func TestRunnerReadsWhatTheWriterCommitted(t *testing.T) {
	path, writer := gateDatabase(t, `CREATE TABLE runs(id TEXT);`)
	if _, err := writer.Exec(`INSERT INTO runs VALUES('run-1')`); err != nil {
		t.Fatal(err)
	}

	result, _ := read(t, path, "SELECT id FROM runs")

	if string(result.Stdout) != "[{\"id\":\"run-1\"}]\n" {
		t.Fatalf("stdout = %q", result.Stdout)
	}
}

func TestRunnerRefusesAsSqlite3Does(t *testing.T) {
	path, writer := gateDatabase(t, `CREATE TABLE runs(id TEXT); INSERT INTO runs VALUES('run-1');`)
	for _, test := range []struct{ name, statement, refusal string }{
		{"a table the database lacks", "SELECT id FROM steps", "no such table: steps"},
		{"a write", "UPDATE runs SET id='run-2'", "readonly database"},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, next := read(t, path, test.statement)

			if len(next.requests) != 0 || result.ExitCode != 1 || !strings.HasPrefix(string(result.Stderr), "Error: ") || !strings.Contains(string(result.Stderr), test.refusal) {
				t.Fatalf("result = %+v, started %v", result, next.requests)
			}
		})
	}
	var id string
	if err := writer.QueryRow(`SELECT id FROM runs`).Scan(&id); err != nil || id != "run-1" {
		t.Fatalf("the database holds %q, %v", id, err)
	}
}

func TestRunnerRefusesADatabaseThatIsNotThere(t *testing.T) {
	result, next := read(t, filepath.Join(t.TempDir(), "state.sqlite"), "SELECT 1")

	if len(next.requests) != 0 || result.ExitCode != 1 || len(result.Stderr) == 0 {
		t.Fatalf("result = %+v, started %v", result, next.requests)
	}
}

func TestRunnerEndsAReadItsContextEnded(t *testing.T) {
	path, _ := gateDatabase(t, `CREATE TABLE runs(id TEXT);`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := (Runner{Next: &refusingRunner{}}).Run(ctx, execx.Request{Name: "sqlite3", Args: []string{"-readonly", "-json", path, "SELECT id FROM runs"}})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestRunnerRunsEveryOtherRequestWithNext(t *testing.T) {
	for _, req := range []execx.Request{
		{Name: "git", Args: []string{"rev-parse", "HEAD"}},
		{Name: "sqlite3", Args: []string{"-cmd", ".timeout 5000", "-json", `C:\nm\state.sqlite`, "BEGIN IMMEDIATE; UPDATE runs SET id='x'; COMMIT;"}},
		{Name: "sqlite3", Args: []string{"-readonly", "-json", "state.sqlite", "SELECT 1"}},
		{Name: "sqlite3", Args: []string{"-readonly", "-json", `C:\nm\state.sqlite`}},
	} {
		t.Run(strings.Join(append([]string{req.Name}, req.Args...), " "), func(t *testing.T) {
			next := &refusingRunner{}

			_, err := (Runner{Next: next}).Run(context.Background(), req)

			if err == nil || len(next.requests) != 1 || !reflect.DeepEqual(next.requests[0], req) {
				t.Fatalf("err = %v, Next ran %v", err, next.requests)
			}
		})
	}
}
