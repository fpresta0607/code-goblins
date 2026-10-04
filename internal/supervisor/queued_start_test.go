package supervisor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

func TestCompletedOutcomeLeavesTheBoardQueueAndRefusesStart(t *testing.T) {
	for _, row := range []string{"- **next-task** - Delivered", "- [ ] next-task - Delivered"} {
		t.Run(row, func(t *testing.T) {
			// Arrange
			spawner := &spawnRecorder{}
			handler, h := startBoard(t, 8*gigabyte, spawner)
			queueBriefedTask(t, h, row+" (repo: code-goblins)", plainBrief)
			if ids := snapshotIDs(t, handler, queued); len(ids) != 1 {
				t.Fatalf("new work missing before completion: %v", ids)
			}
			if err := state.WriteOutcome(h.State, state.Outcome{ID: "next-task", Generation: "old", Title: "Delivered", Phase: "done", Evidence: "reported pull request", At: time.Now().UTC()}); err != nil {
				t.Fatal(err)
			}
			if err := handler.Service.refreshHistory(t.Context(), time.Now().UTC()); err != nil {
				t.Fatal(err)
			}

			// Act
			ids := snapshotIDs(t, handler, queued)
			response := postStart(handler, `{"task":"next-task"}`, "board.local", "http://board.local", orderToken)

			// Assert
			if len(ids) != 0 {
				t.Errorf("completed task still renders runnable: %v", ids)
			}
			if response.Code != 409 || !strings.Contains(response.Body.String(), "not queued") {
				t.Errorf("completed start=%d %s", response.Code, response.Body)
			}
			if response.Code == 202 {
				waitStarted(t, handler, "next-task")
			}
			if len(spawner.recorded()) != 0 {
				t.Error("completed task was dispatched")
			}
			view, err := handler.Service.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, task := range view.Tasks {
				if task.ID == "finished:next-task" && task.Archived && task.Phase == "done" {
					found = true
				}
			}
			if !found {
				t.Error("completed history disappeared")
			}
		})
	}
}

func TestStartCreatesAMissingBriefFromTheQueuedTask(t *testing.T) {
	spawner := &spawnRecorder{}
	handler, h := startBoard(t, 5*gigabyte, spawner)
	writeFile(t, filepath.Join(h.Data, "backlog.md"), "## Queued\n- **next-task** - Improve search (repo: code-goblins, mode: direct-PR)\n  Search by project name.\n")
	response := postStart(handler, `{"task":"next-task"}`, "board.local", "http://board.local", orderToken)
	if response.Code != 202 {
		t.Fatalf("start=%d %s", response.Code, response.Body)
	}
	waitStarted(t, handler, "next-task")
	path := filepath.Join(h.Data, "next-task", "brief.md")
	if project := briefProject(path); project != "code-goblins" {
		t.Fatalf("generated project=%q", project)
	}
	if mode := briefSettings(path)["mode"]; mode != "direct-PR" {
		t.Fatalf("generated mode=%q", mode)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// The emitted brief is the goblin's instruction interface.
	for _, text := range []string{"Improve search", "Search by project name.", "## Acceptance criteria", "## Constraints", "## Authentication", "## Commits", "## Delivery"} {
		if !strings.Contains(string(data), text) {
			t.Errorf("brief omits %q", text)
		}
	}
	if len(spawner.recorded()) != 1 {
		t.Fatal("start did not dispatch once")
	}
	records, err := wake.Pending(h.State)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, record := range records {
		if strings.Contains(record.Detail, "brief created") {
			found = true
		}
	}
	if !found {
		t.Fatal("CFO was not told a brief was created")
	}
}

func TestStartRefusesAnUnresolvedBlockerEvenWithABrief(t *testing.T) {
	for _, blocker := range []string{"overlord", "2026-10-01", "dependency"} {
		t.Run(blocker, func(t *testing.T) {
			spawner := &spawnRecorder{}
			handler, h := startBoard(t, 5*gigabyte, spawner)
			queueBriefedTask(t, h, "- **next-task** - Ship it (repo: code-goblins) blocked-by: "+blocker, plainBrief)
			response := postStart(handler, `{"task":"next-task"}`, "board.local", "http://board.local", orderToken)
			if response.Code == 202 {
				waitStarted(t, handler, "next-task")
			}
			if response.Code != 409 || !strings.Contains(response.Body.String(), blocker) {
				t.Fatalf("blocked start=%d %s", response.Code, response.Body)
			}
			if len(spawner.recorded()) != 0 {
				t.Fatal("blocked task started")
			}
		})
	}
}
