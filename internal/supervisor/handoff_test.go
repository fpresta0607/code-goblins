package supervisor

import (
	"encoding/json"
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
	tasks := finishedTasks(home.State, time.Now())
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
