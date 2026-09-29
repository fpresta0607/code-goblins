package lifecycle

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
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
				if err := fleet.WriteQueuedBrief(h, queued); err != nil {
					t.Fatal(err)
				}
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

func TestStopQueuedKeepsTheTaskBrief(t *testing.T) {
	for _, test := range []struct {
		name, existing string
		want           []string
	}{
		{name: "missing brief", want: []string{"## Project\n\nexample", "Fix login", "First detail line.\nSecond detail line."}},
		{name: "existing brief", existing: "# Brief task\n\nThe CFO's own brief.\n", want: []string{"# Brief task\n\nThe CFO's own brief.\n"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := home.Home{State: t.TempDir(), Data: t.TempDir()}
			backlog := filepath.Join(h.Data, "backlog.md")
			if err := os.WriteFile(backlog, []byte("## Queued\n- **task** - Fix login (repo: example)\n  First detail line.\n  Second detail line.\n- **next** - Leave this task\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			brief := filepath.Join(h.Data, "task", "brief.md")
			if test.existing != "" {
				if err := os.MkdirAll(filepath.Dir(brief), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(brief, []byte(test.existing), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			record, err := StopQueued(h, Request{ID: "task", Operation: "stop-1", Action: "stop"}, "")

			if err != nil || record.Phase != "stopped" || len(record.Kept) != 1 || record.Kept[0] != "task brief" {
				t.Fatalf("stop=%+v %v", record, err)
			}
			kept, err := os.ReadFile(brief)
			if err != nil {
				t.Fatal("stop lost the task brief:", err)
			}
			for _, want := range test.want {
				if !strings.Contains(string(kept), want) {
					t.Errorf("brief lacks %q: %s", want, kept)
				}
			}
			if test.existing != "" && string(kept) != test.existing {
				t.Errorf("existing brief changed: %q", kept)
			}
			if _, err := fleet.ReadQueuedTask(h, "task"); err == nil {
				t.Fatal("stopped task is still queued")
			}
			if _, err := fleet.ReadQueuedTask(h, "next"); err != nil {
				t.Fatal("stop removed the unrelated task", err)
			}
		})
	}
}

func TestStopQueuedKeepsTheRowWhenItsBriefCannotBeWritten(t *testing.T) {
	h := home.Home{State: t.TempDir(), Data: t.TempDir()}
	backlog := filepath.Join(h.Data, "backlog.md")
	content := []byte("## Queued\n- **task** - Fix login (repo: example)\n  Only copy of this detail.\n")
	if err := os.WriteFile(backlog, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.Data, "task"), []byte("not a folder"), 0o600); err != nil {
		t.Fatal(err)
	}

	record, err := StopQueued(h, Request{ID: "task", Operation: "stop-1", Action: "stop"}, "")

	if err == nil {
		t.Fatalf("stop claimed success without a brief: %+v", record)
	}
	if got, err := os.ReadFile(backlog); err != nil || string(got) != string(content) {
		t.Fatalf("row changed after the brief failed: %q %v", got, err)
	}
	if _, err := state.ReadLifecycle(h.State, "task"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a lifecycle record claims the stop: %v", err)
	}
	if _, err := state.ReadOutcome(h.State, "task"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("an outcome claims the stop: %v", err)
	}
}

func TestStopQueuedRecordsFailureWhenRemovalIsBlocked(t *testing.T) {
	h := home.Home{State: t.TempDir(), Data: t.TempDir()}
	backlog := filepath.Join(h.Data, "backlog.md")
	content := []byte("## Queued\n- **task** - Fix login (repo: example)\n  Keep this detail.\n")
	if err := os.WriteFile(backlog, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := lock.AcquireExclusiveNamed(h.State, ".backlog.lock"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := lock.ReleaseExclusiveNamed(h.State, ".backlog.lock"); err != nil {
			t.Error(err)
		}
	})

	record, err := StopQueued(h, Request{ID: "task", Operation: "stop-1", Action: "stop"}, "")

	if !errors.Is(err, lock.ErrHeld) {
		t.Fatalf("stop error=%v, want backlog lock refusal", err)
	}
	if record.Phase != "failed" || len(record.Problems) != 1 || !strings.Contains(record.Problems[0], lock.ErrHeld.Error()) {
		t.Errorf("failed stop=%+v", record)
	}
	saved, err := state.ReadLifecycle(h.State, "task")
	if err != nil || saved.Phase != "failed" || len(saved.Problems) != 1 || !strings.Contains(saved.Problems[0], lock.ErrHeld.Error()) || !saved.NoticeSent {
		t.Errorf("saved failure=%+v %v", saved, err)
	}
	if got, err := os.ReadFile(backlog); err != nil || string(got) != string(content) {
		t.Errorf("row changed after removal failed: %q %v", got, err)
	}
	brief, err := os.ReadFile(filepath.Join(h.Data, "task", "brief.md"))
	if err != nil || !strings.Contains(string(brief), "Keep this detail.") {
		t.Errorf("brief lost after removal failed: %q %v", brief, err)
	}
	if _, err := state.ReadOutcome(h.State, "task"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("an outcome claims the stop: %v", err)
	}
	if _, err := os.Stat(filepath.Join(h.State, "task.status")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a status log marks the queued task as ended: %v", err)
	}
}

func TestStopQueuedReplaysAFailedOperationAndAcceptsAFreshOne(t *testing.T) {
	h := home.Home{State: t.TempDir(), Data: t.TempDir()}
	backlog := filepath.Join(h.Data, "backlog.md")
	content := []byte("## Queued\n- **task** - Fix login (repo: example)\n")
	if err := os.WriteFile(backlog, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := lock.AcquireExclusiveNamed(h.State, ".backlog.lock"); err != nil {
		t.Fatal(err)
	}
	failed := Request{ID: "task", Operation: "stop-1", Action: "stop", Reason: "Not needed"}
	if _, err := StopQueued(h, failed, ""); !errors.Is(err, lock.ErrHeld) {
		t.Fatalf("stop error=%v, want backlog lock refusal", err)
	}
	if err := lock.ReleaseExclusiveNamed(h.State, ".backlog.lock"); err != nil {
		t.Fatal(err)
	}

	replayed, replayErr := StopQueued(h, failed, "")

	if replayErr == nil || !strings.Contains(replayErr.Error(), lock.ErrHeld.Error()) || replayed.Phase != "failed" {
		t.Errorf("replayed stop=%+v %v, want the recorded failure", replayed, replayErr)
	}
	if got, err := os.ReadFile(backlog); err != nil || string(got) != string(content) {
		t.Errorf("replayed failure removed the row: %q %v", got, err)
	}
	if pending, err := wake.Pending(h.State); err != nil || len(pending) != 1 {
		t.Errorf("notifications=%+v %v, want the one failure notice", pending, err)
	}

	fresh, freshErr := StopQueued(h, Request{ID: "task", Operation: "stop-2", Action: "stop", Reason: "Not needed"}, "")

	if freshErr != nil || fresh.Phase != "stopped" || !fresh.NoticeSent {
		t.Errorf("fresh stop=%+v %v", fresh, freshErr)
	}
	if _, err := fleet.ReadQueuedTask(h, "task"); !errors.Is(err, fleet.ErrNotQueued) {
		t.Errorf("fresh stop kept the row: %v", err)
	}
	pending, err := wake.Pending(h.State)
	if err != nil || len(pending) != 2 {
		t.Errorf("notifications=%+v %v, want one failure and one stop", pending, err)
	}
}
