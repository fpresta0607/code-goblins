package main

import (
	"bytes"
	"context"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// runningTask writes the record of a task that was dispatched with no title,
// with a key the task's typed record does not carry.
func runningTask(t *testing.T, h home.Home, id string) map[string]string {
	t.Helper()
	record := map[string]string{"harness": "claude", "kind": "ship", "mode": "direct-PR", "project": `C:\northwind-api`, "spawn_gen": "s1", "pr": "https://github.com/fpresta0607/northwind-api/pull/412"}
	if err := os.MkdirAll(h.State, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteMeta(filepath.Join(h.State, id+".meta"), record); err != nil {
		t.Fatal(err)
	}
	return record
}

func TestTitleNamesARunningTaskAndKeepsTheRestOfItsRecord(t *testing.T) {
	// Arrange
	h := testHome(t)
	want := maps.Clone(runningTask(t, h, "nw-sync"))
	want["title"] = "Say why a billing sync fails"
	var stdout, stderr bytes.Buffer

	// Act
	exit := runWithRuntime([]string{"title", "nw-sync", "  Say why a billing   sync fails "}, &stdout, &stderr, testCommandRuntimeForHome(h))

	// Assert
	got, err := state.ReadMeta(filepath.Join(h.State, "nw-sync.meta"))
	if exit != 0 || err != nil || !maps.Equal(got, want) {
		t.Fatalf("exit = %d, record = %v, %v, want %v; stderr=%s", exit, got, err, want, stderr.String())
	}
	if stdout.String() != "titled nw-sync: Say why a billing sync fails\n" || stderr.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q, want the new title confirmed", stdout.String(), stderr.String())
	}
}

func TestTitleRefusesWhatItCannotUseAndChangesNothing(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantExit int
		wantSays string
	}{
		{name: "no title", args: []string{"title", "nw-sync"}, wantExit: 2, wantSays: "cfo title <id>"},
		{name: "a third word", args: []string{"title", "nw-sync", "Say why", "a sync fails"}, wantExit: 2, wantSays: "cfo title <id>"},
		{name: "an empty title", args: []string{"title", "nw-sync", "   "}, wantExit: 2, wantSays: "empty"},
		{name: "a title of several lines", args: []string{"title", "nw-sync", "Say why a billing sync fails\nThe sync task swallows the error"}, wantExit: 2, wantSays: "one line"},
		{name: "a title longer than a title", args: []string{"title", "nw-sync", strings.Repeat("why ", 30)}, wantExit: 2, wantSays: "100 characters"},
		{name: "a task that is not running", args: []string{"title", "nw-queued", "Refund totals in the export"}, wantExit: 1, wantSays: "nw-queued"},
		{name: "an id that names no task", args: []string{"title", `..\nw-sync`, "Say why a billing sync fails"}, wantExit: 1, wantSays: "task ID"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			h := testHome(t)
			want := runningTask(t, h, "nw-sync")
			var stdout, stderr bytes.Buffer

			// Act
			exit := runWithRuntime(tc.args, &stdout, &stderr, testCommandRuntimeForHome(h))

			// Assert
			if exit != tc.wantExit || !strings.Contains(stderr.String(), tc.wantSays) || stdout.Len() != 0 {
				t.Fatalf("exit = %d, stdout=%q, stderr=%q, want exit %d naming %q", exit, stdout.String(), stderr.String(), tc.wantExit, tc.wantSays)
			}
			got, err := state.ReadMeta(filepath.Join(h.State, "nw-sync.meta"))
			if err != nil || !maps.Equal(got, want) {
				t.Fatalf("record = %v, %v, want it untouched: %v", got, err, want)
			}
			if entries, _ := os.ReadDir(h.State); len(entries) != 1 {
				t.Fatalf("the state directory holds %d entries, want only the running task's record", len(entries))
			}
		})
	}
}

func TestRunSpawnTakesTheTitleItIsGivenOverTheBacklogRows(t *testing.T) {
	cases := []struct {
		name    string
		backlog string
	}{
		{name: "a task with no backlog row"},
		{name: "a task whose backlog row has a title of its own", backlog: "## Queued\n\n- **nw-sync** - An older title (repo: northwind-api)\n"},
		{name: "a task queued in a row with no title", backlog: "## Queued\n\n- next, after the export work: nw-sync (brief data/nw-sync/brief.md)\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			h := testHome(t)
			if tc.backlog != "" {
				if err := os.MkdirAll(h.Data, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(h.Data, "backlog.md"), []byte(tc.backlog), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			deps := testCommandRuntimeForHome(h)
			var got *spawn.Request
			deps.spawn = func(_ context.Context, _ home.Home, request spawn.Request) (spawn.Result, error) {
				got = &request
				return spawn.Result{Output: "spawned nw-sync"}, nil
			}
			var stdout, stderr bytes.Buffer

			// Act
			exit := runWithRuntime([]string{"spawn", "nw-sync", "--project", `C:\project`, "--brief", briefFile(t), "--harness", "claude", "--title", " Say why a billing  sync fails "}, &stdout, &stderr, deps)

			// Assert
			if exit != 0 || got == nil || got.Title != "Say why a billing sync fails" {
				t.Fatalf("exit=%d request=%+v, want the given title; stderr=%s", exit, got, stderr.String())
			}
		})
	}
}

func TestRunSpawnRefusesATitleThatIsNotOneBeforeAnythingStarts(t *testing.T) {
	for name, title := range map[string]string{
		"several lines": "Say why a billing sync fails\nThe sync task swallows the error",
		"too long":      strings.Repeat("why ", 30),
		"only spaces":   "   ",
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			deps := testCommandRuntime(t)
			called := false
			deps.spawn = func(context.Context, home.Home, spawn.Request) (spawn.Result, error) {
				called = true
				return spawn.Result{}, nil
			}
			var stdout, stderr bytes.Buffer

			// Act
			exit := runWithRuntime([]string{"spawn", "nw-sync", "--project", `C:\project`, "--brief", briefFile(t), "--harness", "claude", "--title", title}, &stdout, &stderr, deps)

			// Assert
			if exit != 2 || called || !strings.Contains(stderr.String(), "--title") {
				t.Fatalf("exit = %d, spawn called = %v, stderr = %q; want the title refused before any spawn", exit, called, stderr.String())
			}
		})
	}
}
