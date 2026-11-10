package supervisor

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
	for _, key := range []string{"harness", "model", "effort"} {
		if value := briefSettings(path)[key]; value != "" {
			t.Errorf("generated brief pins implicit %s=%s", key, value)
		}
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

// Issue 1925: a queued task's detail is task text, so a line of it that reads
// as a launch choice never replaces what the brief's Delivery section names,
// on the first start or on the one after a failed spawn.
func TestStartAfterAFailedSpawnKeepsTheDeliveryChoicesOverTheTaskDetail(t *testing.T) {
	// Arrange
	spawner := &spawnRecorder{output: "cfo spawn: the terminal did not open\n", err: errors.New("exit status 1")}
	handler, h := startBoard(t, 5*gigabyte, spawner)
	writeFile(t, filepath.Join(h.Data, "backlog.md"), "## Queued\n- **next-task** - Improve search (repo: code-goblins)\n  Search by project name.\n  mode: direct-PR\n  harness: codex\n  model: quoted-model\n  effort: low\n")
	path := filepath.Join(h.Data, "next-task", "brief.md")

	// Act
	var calls [][]string
	for start := 1; start <= 2; start++ {
		if response := postStart(handler, `{"task":"next-task"}`, "board.local", "http://board.local", orderToken); response.Code != 202 {
			t.Fatalf("start %d=%d %s", start, response.Code, response.Body)
		}
		calls = awaitDispatch(t, handler.Service, spawner, start)
	}
	card := cardOf(t, handler, "next-task")

	// Assert
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	task, delivery, _ := strings.Cut(string(data), "\n## Delivery\n")
	for _, line := range []string{"mode: direct-PR", "harness: codex", "model: quoted-model", "effort: low"} {
		if !slices.Contains(strings.Split(task, "\n"), line) {
			t.Fatalf("the brief's task text holds no bare %q line, so this test proves nothing: %q", line, task)
		}
	}
	if !slices.Contains(strings.Split(delivery, "\n"), "mode: no-mistakes") {
		t.Fatalf("delivery section=%q, want mode: no-mistakes", delivery)
	}
	if len(calls) != 2 {
		t.Fatalf("cfo spawn ran %d times, want twice: %q", len(calls), calls)
	}
	spawn := []string{"spawn", "next-task", "--project", "code-goblins", "--brief", path}
	revision := []string{"--brief-revision", fmt.Sprintf("brief:%x", sha256.Sum256(data))}
	if first := slices.Concat(spawn, revision); !slices.Equal(calls[0], first) {
		t.Errorf("first start ran cfo %q, want cfo %q", calls[0], first)
	}
	if again := slices.Concat(spawn, []string{"--mode", "no-mistakes"}, revision); !slices.Equal(calls[1], again) {
		t.Errorf("the start after the failed spawn ran cfo %q, want cfo %q", calls[1], again)
	}
	if card.Mode != "no-mistakes" || card.Harness != "" || card.Model != "" || card.Effort != "" {
		t.Errorf("queued card shows %s on %s/%s/%s, want no-mistakes on the routed engine", card.Mode, card.Harness, card.Model, card.Effort)
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
