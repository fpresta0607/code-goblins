package supervisor

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/verify"
)

// gateHead is the head of goblin cg-wakes's pull request 209 in hostedRollup.
const gateHead = "3d7072f8aa"

// localRun is a cfo gate test report of project northwind, with its log in
// the store's folder for the project: the run of commit for task (empty for
// a gate's run, which names none), with the packages that failed.
func localRun(t *testing.T, store, task, commit, status string, start time.Time, failed ...string) verify.Report {
	t.Helper()
	dir := filepath.Join(store, "reports", "northwind")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(dir, start.Format("20060102T150405Z")+"-"+commit+"-affected-1.log")
	if err := os.WriteFile(log, []byte("ok  \tgithub.com/o/northwind/internal/a\t2.1s\n--- FAIL: TestSync (0.20s)\n    sync_test.go:12: token: s3cret-value\nFAIL\tgithub.com/o/northwind/internal/b\t3.4s\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	packages := []verify.PackageResult{{Package: "github.com/o/northwind/internal/a", Status: "passed"}}
	for _, name := range failed {
		packages = append(packages, verify.PackageResult{Package: "github.com/o/northwind/" + name, Status: "failed", Failed: []string{"TestSync"}})
	}
	return verify.Report{
		Version: verify.ReportVersion, Project: "northwind", Task: task, Commit: commit, Level: "affected", RequiredLevel: "affected",
		Status: status, Start: start, DurationSeconds: 1080, QueueSeconds: 300, Log: log,
		Checks: []verify.Result{{Command: []string{"go", "vet"}, Status: "passed"}, {Command: []string{"go", "test", "-json"}, Status: status, Packages: packages}},
	}
}

// localBoard is a board with live goblin cg-wakes working in project
// northwind on pull request 209, whose checks hostedRollup reports, and
// another goblin, cg-other, in the same project; its cfo gate test reports are
// reports, newest first.
func localBoard(t *testing.T, reports ...verify.Report) *HTTP {
	t.Helper()
	handler, h := orderBoard(t)
	project := filepath.Join(t.TempDir(), "northwind")
	for _, id := range []string{"cg-wakes", "cg-other"} {
		liveGoblin(t, h, id, project)
	}
	if err := state.AppendStatus(h.State, "cg-wakes", "done: PR https://github.com/o/r/pull/209"); err != nil {
		t.Fatal(err)
	}
	forge := forgeFor(project, "cg-wakes")
	forge.branch, forge.runs, forge.pulls = "feat/wakes", "[]", hostedRollup("", testPassed)
	handler.Service.Options.CI = forge
	handler.Service.Options.VerifyReports = func(name string) ([]verify.Report, error) {
		if name != "northwind" {
			t.Errorf("reports were read for project %q, want northwind", name)
		}
		return reports, nil
	}
	now := time.Date(2026, 9, 30, 12, 31, 0, 0, time.UTC)
	if err := handler.Service.checkFleet(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	handler.Service.cycle(context.Background(), false)
	return handler
}

// cardLocal reads the cfo gate test run the board's snapshot shows on a
// task's card, as the JSON the board reads.
func cardLocal(t *testing.T, s *Service, id string) map[string]any {
	t.Helper()
	snapshot, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	index := slices.IndexFunc(snapshot.Tasks, func(task Task) bool { return task.ID == id })
	if index < 0 {
		t.Fatalf("tasks = %+v, want %s", snapshot.Tasks, id)
	}
	data, err := json.Marshal(snapshot.Tasks[index])
	if err != nil {
		t.Fatal(err)
	}
	var card map[string]any
	if err := json.Unmarshal(data, &card); err != nil {
		t.Fatal(err)
	}
	checks, _ := card["local_checks"].(map[string]any)
	return checks
}

// A card shows the newest cfo gate test run of its task's change: one a
// gate ran on the commit its pull request is at, which names no task, or one
// its goblin ran, which does; never another task's. It gives the level, the
// result, how long the run took and waited, and what did not pass.
func TestLocalChecksShowTheNewestRunOfATasksChange(t *testing.T) {
	// Arrange
	store := t.TempDir()
	start := time.Date(2026, 9, 30, 11, 0, 0, 0, time.UTC)
	handler := localBoard(t,
		localRun(t, store, "cg-other", "aaaaaaaaaa", "passed", start.Add(3*time.Hour)),
		localRun(t, store, "", gateHead, "failed", start.Add(2*time.Hour), "internal/b"),
		localRun(t, store, "cg-wakes", "bbbbbbbbbb", "passed", start.Add(time.Hour)),
	)

	// Act
	wakes := cardLocal(t, handler.Service, "cg-wakes")
	other := cardLocal(t, handler.Service, "cg-other")

	// Assert
	delete(wakes, "at")
	got, _ := json.Marshal(wakes)
	want := `{"commit":"3d7072f8aa","duration_seconds":1080,"failed":["internal/b"],"level":"affected","queue_seconds":300,"required_level":"affected","status":"failed"}`
	if string(got) != want {
		t.Errorf("cg-wakes's card = %s, want the gate's run of its pull request's head: %s", got, want)
	}
	if other["commit"] != "aaaaaaaaaa" || other["status"] != "passed" {
		t.Errorf("cg-other's card = %v, want its own run", other)
	}
}

// A task whose change no kept run tested shows none, and a goblin's own run
// still shows once its gate's has gone.
func TestLocalChecksShowNothingWithoutARunOfTheTasksChange(t *testing.T) {
	// Arrange
	store := t.TempDir()
	start := time.Date(2026, 9, 30, 11, 0, 0, 0, time.UTC)
	handler := localBoard(t, localRun(t, store, "cg-other", "aaaaaaaaaa", "passed", start))

	// Act
	wakes := cardLocal(t, handler.Service, "cg-wakes")

	// Assert
	if wakes != nil {
		t.Errorf("cg-wakes's card = %v, want no run: none of the kept runs is of its change", wakes)
	}
}

// The board serves the end of the log of the run a task's card shows,
// redacted, and nothing for a task with no run or for a report whose log is
// outside its project's folder of the store.
func TestLocalChecksLogServesTheEndOfTheRunsLog(t *testing.T) {
	// Arrange
	store := t.TempDir()
	start := time.Date(2026, 9, 30, 11, 0, 0, 0, time.UTC)
	elsewhere := localRun(t, store, "cg-other", "aaaaaaaaaa", "failed", start)
	elsewhere.Log = filepath.Join(store, "elsewhere.log")
	if err := os.WriteFile(elsewhere.Log, []byte("not a run's log\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	handler := localBoard(t, localRun(t, store, "", gateHead, "failed", start.Add(time.Hour), "internal/b"), elsewhere)
	read := func(id string) (int, string) {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", "http://board.local/api/tasks/"+id+"/checks", nil))
		return response.Code, response.Body.String()
	}

	// Act
	code, body := read("cg-wakes")
	outsideCode, outsideBody := read("cg-other")

	// Assert
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if code != 200 {
		t.Fatalf("the log = %d %s, want 200 with its lines", code, body)
	}
	if !slices.Contains(lines, "--- FAIL: TestSync (0.20s)") || strings.Contains(body, "s3cret-value") || !strings.Contains(body, "token: [redacted]") {
		t.Errorf("the log's lines = %q, want the run's failure with the token redacted", lines)
	}
	if outsideCode != 404 || strings.Contains(outsideBody, "not a run's log") {
		t.Errorf("a log outside its project's folder = %d %s, want 404 and nothing of it", outsideCode, outsideBody)
	}
}
