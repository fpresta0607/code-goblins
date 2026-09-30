package supervisor

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestFinishedTaskKeepsItsReportBeforeTheCleanupRecord(t *testing.T) {
	_, home := testStore(t)
	for _, report := range []string{"done: PR https://example.com/pull/218", "done: returned worktree C:/scratch/work via cfo cleanup"} {
		if err := state.AppendStatus(home.State, "retired-task", report); err != nil {
			t.Fatal(err)
		}
	}
	tasks := finishedTasks(home, time.Now())
	data, err := json.Marshal(tasks)
	if err != nil {
		t.Fatal(err)
	}
	var got []struct {
		LastReport string    `json:"last_report"`
		RetiredAt  time.Time `json:"retired_at"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].LastReport != "done: PR https://example.com/pull/218" {
		t.Fatalf("retired history lost the goblin's own report: %s", data)
	}
	lines, err := state.TailStatus(home.State, "retired-task", 2)
	if err != nil {
		t.Fatal(err)
	}
	retired, _ := state.SplitStatus(lines[len(lines)-1])
	if !got[0].RetiredAt.Equal(retired) {
		t.Fatalf("retirement time = %v, want cleanup event %v", got[0].RetiredAt, retired)
	}
}

func TestTaskHandoffAvailabilityAndLastReportReachTheSnapshot(t *testing.T) {
	store, home := testStore(t)
	path := filepath.Join(home.Data, "task-1", "handoff.md")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("Work retained."), 0600); err != nil {
		t.Fatal(err)
	}
	if err := state.AppendStatus(home.State, "task-1", "working: checks passed"); err != nil {
		t.Fatal(err)
	}
	if err := state.AppendStatus(home.State, "task-1", "pipeline-findings-accepted: audit record"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := (&Service{Store: store}).Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(snapshot.Tasks[0])
	if err != nil {
		t.Fatal(err)
	}
	var task struct {
		Handoff    bool   `json:"handoff"`
		LastReport string `json:"last_report"`
	}
	if err := json.Unmarshal(data, &task); err != nil {
		t.Fatal(err)
	}
	if !task.Handoff || task.LastReport != "working: checks passed" {
		t.Fatalf("task has no readable handoff or the wrong last report: %s", data)
	}
}

func TestTaskSessionSummaryDoesNotInventEvidence(t *testing.T) {
	spawned := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	for _, fixture := range []struct {
		name    string
		lines   []string
		spawned time.Time
		report  string
	}{
		{"missing report", nil, time.Time{}, ""},
		{"unstamped cleanup", []string{"done: PR https://example.com/pull/218", "done: returned worktree C:/scratch via cfo cleanup"}, time.Time{}, "done: PR https://example.com/pull/218"},
		{"a later live report", []string{"2026-09-30T09:00:00Z done: returned worktree C:/scratch via cfo cleanup", "2026-09-30T11:00:00Z working: resumed"}, time.Time{}, "working: resumed"},
		{"old generation", []string{"2026-09-30T09:00:00Z done: PR https://example.com/pull/218"}, spawned, ""},
		{"redacted report", []string{"working: password=fixture-secret"}, time.Time{}, "working: password=[redacted]"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			report, retired := taskSessionSummary(fixture.lines, fixture.spawned)
			if report != fixture.report || !retired.IsZero() {
				t.Fatalf("summary = %q, %v; want %q with no known retirement time", report, retired, fixture.report)
			}
		})
	}
}

func TestTaskHandoffServesExistingNotesAsRedactedPlainText(t *testing.T) {
	for _, folder := range []string{"task-1", "archive/finished/retired-task"} {
		t.Run(folder, func(t *testing.T) {
			store, home := testStore(t)
			path := filepath.Join(home.Data, filepath.FromSlash(folder), "handoff.md")
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("# Handoff\nWork retained.\npassword=fixture-secret\n<script>alert(1)</script>"), 0600); err != nil {
				t.Fatal(err)
			}
			handler := NewHTTP(&Service{Store: store}, "board.local", nil)
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, httptest.NewRequest("GET", "http://board.local/api/tasks/"+filepath.Base(folder)+"/handoff", nil))

			if response.Code != 200 || response.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
				t.Fatalf("handoff returned %d (%s): %s", response.Code, response.Header().Get("Content-Type"), response.Body.String())
			}
			if !strings.Contains(response.Body.String(), "Work retained.") || strings.Contains(response.Body.String(), "fixture-secret") || !strings.Contains(response.Body.String(), "[redacted]") {
				t.Fatalf("wrong or unredacted handoff: %q", response.Body.String())
			}
		})
	}
}

func TestTaskHandoffRefusesMissingNonregularAndOversizedNotes(t *testing.T) {
	for _, kind := range []string{"missing", "directory", "large"} {
		t.Run(kind, func(t *testing.T) {
			store, home := testStore(t)
			path := filepath.Join(home.Data, "task-1", "handoff.md")
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if kind == "directory" {
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "large" {
				if err := os.WriteFile(path, []byte(strings.Repeat("x", (1<<20)+1)), 0600); err != nil {
					t.Fatal(err)
				}
			}
			handler := NewHTTP(&Service{Store: store}, "board.local", nil)
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, httptest.NewRequest("GET", "http://board.local/api/tasks/task-1/handoff", nil))

			if response.Code == 200 || response.Body.Len() > 1000 {
				t.Fatalf("%s handoff served: %d (%d bytes)", kind, response.Code, response.Body.Len())
			}
		})
	}
}

func TestTaskHandoffDoesNotBorrowAnArchivedRunForAReusedLiveID(t *testing.T) {
	store, home := testStore(t)
	path := filepath.Join(home.Data, "archive", "finished", "task-1", "handoff.md")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("Old run's handoff"), 0600); err != nil {
		t.Fatal(err)
	}
	handler := NewHTTP(&Service{Store: store}, "board.local", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, httptest.NewRequest("GET", "http://board.local/api/tasks/task-1/handoff", nil))

	if response.Code != 404 {
		t.Fatalf("reused live task borrowed old handoff: %d %s", response.Code, response.Body.String())
	}
}

func TestTaskHandoffRejectsPathsAndUntrustedOrigins(t *testing.T) {
	store, _ := testStore(t)
	handler := NewHTTP(&Service{Store: store}, "board.local", nil)
	for _, path := range []string{"/api/tasks/../handoff", "/api/tasks/task-1/../handoff", "/api/tasks/task-1%2F..%2F../handoff"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", "http://board.local"+path, nil))
		if response.Code != 400 {
			t.Fatalf("path %q returned %d", path, response.Code)
		}
	}
	request := httptest.NewRequest("GET", "http://board.local/api/tasks/task-1/handoff", nil)
	request.Header.Set("Origin", "https://untrusted.example")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 403 {
		t.Fatalf("untrusted origin returned %d", response.Code)
	}
}

func TestTaskHandoffReadsOnlyTheCurrentPauseNote(t *testing.T) {
	for _, kind := range []string{"current", "stale", "unsaved", "missing", "credential", "outside", "large"} {
		t.Run(kind, func(t *testing.T) {
			store, home := testStore(t)
			path := filepath.Join(home.State, "tasktmp", "task-1", "pause-operation-1.md")
			if kind == "credential" {
				path = filepath.Join(filepath.Dir(path), "auth.ps1")
			}
			if kind == "outside" {
				path = filepath.Join(t.TempDir(), "pause-operation-1.md")
			}
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			content := "Saved pause note. password=fixture-secret"
			if kind == "large" {
				content = strings.Repeat("x", (1<<20)+1)
			}
			if kind != "missing" {
				if err := os.WriteFile(path, []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			record := state.Lifecycle{ID: "task-1", Generation: "g1", Action: "pause", Phase: "paused", Operation: "operation-1", Updated: time.Now().UTC(), Handoff: path, HandoffSaved: kind != "unsaved"}
			if kind == "stale" {
				record.Generation = "g0"
			}
			if err := state.WriteLifecycle(home.State, record); err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			NewHTTP(&Service{Store: store}, "board.local", nil).ServeHTTP(response, httptest.NewRequest("GET", "http://board.local/api/tasks/task-1/handoff", nil))
			if kind == "current" {
				if response.Code != 200 || response.Body.String() != "Saved pause note. password=[redacted]" {
					t.Fatalf("pause handoff: %d %q", response.Code, response.Body.String())
				}
			} else if response.Code == 200 {
				t.Fatalf("%s pause note was exposed", kind)
			}
			snapshot, err := (&Service{Store: store}).Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			if snapshot.Tasks[0].Handoff != (kind == "current") {
				t.Fatalf("%s handoff availability = %v", kind, snapshot.Tasks[0].Handoff)
			}
		})
	}
}

func TestFinishedTaskKeepsItsSummaryThroughLifecycleHistory(t *testing.T) {
	for _, kind := range []string{"outcome", "lifecycle", "undelivered cleanup"} {
		t.Run(kind, func(t *testing.T) {
			_, home := testStore(t)
			id := "retired-task"
			for _, line := range []string{"working: final checks passed", "stopped: returned worktree C:/scratch/work via cfo cleanup"} {
				if err := state.AppendStatus(home.State, id, line); err != nil {
					t.Fatal(err)
				}
			}
			if kind != "undelivered cleanup" {
				if err := state.WriteOutcome(home.State, state.Outcome{ID: id, Phase: "stopped", At: time.Now().UTC()}); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "lifecycle" {
				if err := state.WriteLifecycle(home.State, state.Lifecycle{ID: id, Operation: "stop-1", Action: "stop", Phase: "stopped", Updated: time.Now().UTC()}); err != nil {
					t.Fatal(err)
				}
			}
			tasks := finishedTasks(home, time.Now())
			if len(tasks) != 1 || tasks[0].LastReport != "working: final checks passed" || tasks[0].RetiredAt.IsZero() {
				t.Fatalf("%s lost last report or retirement evidence: %+v", kind, tasks)
			}
		})
	}
}

func TestFinishedTaskDoesNotBorrowThePreviousGenerationsReport(t *testing.T) {
	_, home := testStore(t)
	now := time.Now().UTC()
	id := "reused-task"
	lines := now.Add(-2*time.Hour).Format(time.RFC3339) + " working: old run's report\n" + now.Format(time.RFC3339) + " stopped: returned worktree C:/scratch via cfo cleanup\n"
	if err := os.WriteFile(filepath.Join(home.State, id+".status"), []byte(lines), 0600); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteOutcome(home.State, state.Outcome{ID: id, Generation: fmt.Sprintf("s%d", now.Add(-time.Hour).UnixNano()), Phase: "stopped", At: now}); err != nil {
		t.Fatal(err)
	}
	tasks := finishedTasks(home, now)
	if len(tasks) != 1 || tasks[0].LastReport != "" || tasks[0].RetiredAt.IsZero() {
		t.Fatalf("reused task borrowed a report: %+v", tasks)
	}
}

func TestTaskHandoffReusesOneArchiveListingPerSnapshot(t *testing.T) {
	store, home := testStore(t)
	write := func(id string) {
		path := filepath.Join(home.Data, "archive", "finished", id, "handoff.md")
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("Handoff for "+id), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"retired-a", "retired-b"} {
		if err := state.AppendStatus(home.State, id, "done: returned worktree C:/scratch/"+id+" via cfo cleanup"); err != nil {
			t.Fatal(err)
		}
	}
	write("retired-a")
	archived := archivedTasks(home)
	file, err := openTaskHandoff(home, "retired-a", archived)
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
	write("retired-b")

	_, err = openTaskHandoff(home, "retired-b", archived)

	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a task in the same snapshot listed the archive again: %v", err)
	}
	snapshot, err := (&Service{Store: store}).Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	available := map[string]bool{}
	for _, task := range snapshot.Tasks {
		available[task.ID] = task.Handoff
	}
	if !available["finished:retired-a"] || !available["finished:retired-b"] {
		t.Fatalf("a new snapshot did not see every saved handoff: %v", available)
	}
}
