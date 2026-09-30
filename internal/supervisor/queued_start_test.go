package supervisor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/wake"
)

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
