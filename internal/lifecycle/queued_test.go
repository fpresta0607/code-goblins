package lifecycle

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

func TestStopQueuedResumesAfterRowRemovalAndReportsOnce(t *testing.T) {
	for _, isInterrupted := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary stop", true: "interrupted stop"}[isInterrupted], func(t *testing.T) {
			h := home.Home{State: t.TempDir(), Data: t.TempDir()}
			path := filepath.Join(h.Data, "backlog.md")
			if err := os.WriteFile(path, []byte("## Queued\n- **task** - Stop this task (repo: example)\n- **next** - Leave this task\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			queued, err := fleet.ReadQueuedTask(h, "task")
			if err != nil {
				t.Fatal(err)
			}
			request := Request{ID: "task", Operation: "stop-1", Action: "stop", Reason: "Not needed"}
			if isInterrupted {
				if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: "task", Operation: request.Operation, Generation: "queued", RequestGeneration: "queued", Action: "stop", Phase: "stopping", Title: queued.Row.Title, Project: queued.Row.Repo, Reason: request.Reason}); err != nil {
					t.Fatal(err)
				}
				if err := fleet.RemoveQueuedTask(h, "task", queued.Revision); err != nil {
					t.Fatal(err)
				}
			}
			for attempt := 0; attempt < 2; attempt++ {
				result, err := StopQueued(h, request, queued.Revision)
				if err != nil || result.Phase != "stopped" || !result.NoticeSent {
					t.Fatalf("stop=%+v %v", result, err)
				}
			}
			pending, err := wake.Pending(h.State)
			if err != nil || len(pending) != 1 {
				t.Fatalf("notifications=%+v %v", pending, err)
			}
			outcome, err := state.ReadOutcome(h.State, "task")
			if err != nil || outcome.Phase != "stopped" || outcome.Title != queued.Row.Title {
				t.Fatalf("outcome=%+v %v", outcome, err)
			}
			if _, err := fleet.ReadQueuedTask(h, "next"); err != nil {
				t.Fatal("stop removed the unrelated task", err)
			}
		})
	}
}

func TestStopQueuedRejectsAnArchivedSessionTargetingAReplacementRow(t *testing.T) {
	h := home.Home{State: t.TempDir(), Data: t.TempDir()}
	path := filepath.Join(h.Data, "backlog.md")
	content := []byte("## Queued\n- **task** - Replacement task (repo: example)\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := StopQueued(h, Request{ID: "task", Generation: "old-session", Operation: "stop-old", Action: "stop"}, "")
	if err == nil {
		t.Fatal("an old active card removed its replacement queued task")
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(content) {
		t.Fatalf("replacement row changed: %q %v", got, err)
	}
}

func TestStopFinishesAnInterruptedArchiveWithoutRemovingAReplacementRow(t *testing.T) {
	h := home.Home{State: t.TempDir(), Data: t.TempDir()}
	path := filepath.Join(h.Data, "backlog.md")
	content := []byte("## Queued\n- **task** - Replacement task (repo: example)\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	request := Request{ID: "task", Generation: "old-session", Operation: "stop-old", Action: "stop"}
	if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: request.ID, Generation: request.Generation, RequestGeneration: request.Generation, Operation: request.Operation, Action: "stop", Phase: "stopping", Kept: []string{"pushed branch"}}); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteOutcome(h.State, state.Outcome{ID: request.ID, Generation: request.Generation, Phase: "stopped"}); err != nil {
		t.Fatal(err)
	}
	record, err := StopQueued(h, request, "")
	if err != nil || record.Phase != "stopped" || len(record.Kept) != 1 || record.Kept[0] != "pushed branch" {
		t.Fatalf("archive retry=%+v %v", record, err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(content) {
		t.Fatalf("replacement row changed: %q %v", got, err)
	}
}
