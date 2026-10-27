package supervisor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/nativehook"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

func testStore(t *testing.T) (*Store, home.Home) {
	t.Helper()
	dir := t.TempDir()
	h := home.Home{Root: dir, State: filepath.Join(dir, "state"), Data: filepath.Join(dir, "data")}
	if err := os.MkdirAll(h.State, 0700); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(dir, "work")
	if err := os.MkdirAll(wt, 0700); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteTaskMeta(h.State, state.TaskMeta{ID: "task-1", Project: wt, Worktree: wt, Harness: "codex", Mode: "no-mistakes", Kind: "ship", Backend: "herdr", SpawnGen: "g1", HerdrSession: "test", HerdrWorkspaceID: "w1", HerdrTabID: "tab1", HerdrPaneID: "p1"}); err != nil {
		t.Fatal(err)
	}
	s, err := Open(h)
	if err != nil {
		t.Fatal(err)
	}
	return s, h
}

func blockStoreWrites(t *testing.T, s *Store) func() {
	t.Helper()
	backup := s.path() + ".test-backup"
	_, statErr := os.Stat(s.path())
	if statErr == nil {
		if err := os.Rename(s.path(), backup); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(s.path(), 0700); err != nil {
		t.Fatal(err)
	}
	return func() {
		if err := os.Remove(s.path()); err != nil {
			t.Fatal(err)
		}
		if statErr == nil {
			if err := os.Rename(backup, s.path()); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestRetentionAdmitsNewSessionsAndPreservesRetiredParent(t *testing.T) {
	s, h := testStore(t)
	now := time.Now().Add(-time.Hour)
	for i := 0; i < maxSessions; i++ {
		id := fmt.Sprintf("codex/old-%d", i)
		s.db.Sessions[id] = Session{ID: id, Phase: "ended", UpdatedAt: now.Add(time.Duration(i) * time.Second)}
		s.db.Tasks[fmt.Sprintf("old-%d", i)] = Evaluation{At: now}
	}
	child := s.db.Sessions["codex/old-1"]
	child.Parent = "codex/old-0"
	s.db.Sessions[child.ID] = child
	old := s.db.Sessions["codex/old-0"]
	old.Generation, old.TaskID, old.Role = "g1", "task-1", "goblin"
	s.db.Sessions[old.ID] = old
	s.db.TaskSessions["task-1"] = old.ID
	s.db.Tasks["task-1"] = Evaluation{Phase: "ready", Generation: "g1", At: now}
	if err := s.save(); err != nil {
		t.Fatal(err)
	}
	start := event(t, h, "SessionStart", "new", "", time.Now())
	start.Role, start.TaskID, start.Generation = "cfo", "", ""
	if err := s.Accept(start); err != nil {
		t.Fatal(err)
	}
	d := s.Snapshot()
	if len(d.Sessions) != maxSessions || d.Sessions[child.ID].Parent != "codex/old-0" {
		t.Fatal("retention lost lineage or exceeded its bound")
	}
	if !slices.Contains(d.Retired, "codex/old-0") {
		t.Fatal("missing retired parent marker")
	}
	service := &Service{Store: s}
	if err := service.reconcileTasks(time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(s.Snapshot().Actions) != 1 || s.Snapshot().Actions[0].TaskID != "task-1" {
		t.Fatal("retired session lost unresolved task reconciliation")
	}
	if _, err := s.Queue(Action{ID: "prune", Kind: "evaluate", TaskID: "task-1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.ProcessOne(context.Background(), func(context.Context, Action) (Evaluation, error) { return Evaluation{Phase: "review"}, nil }); err != nil {
		t.Fatal(err)
	}
	if len(s.Snapshot().Tasks) > maxSessions {
		t.Fatal("task evaluations are unbounded")
	}
}

func TestCompletionWriteFailureNeverResendsExternalDeliveryAfterRestart(t *testing.T) {
	s, h := testStore(t)
	now := time.Now().UTC()
	s.db.Actions = append(s.db.Actions, Action{ID: "external", Kind: "review", TaskID: "task-1", Generation: "g1", Text: "hello", Status: "queued", CreatedAt: now, UpdatedAt: now})
	if err := s.save(); err != nil {
		t.Fatal(err)
	}
	var restore func()
	err := s.ProcessOne(context.Background(), func(context.Context, Action) (Evaluation, error) {
		restore = blockStoreWrites(t, s)
		return Evaluation{Reason: "accepted"}, nil
	})
	if err == nil || s.Snapshot().Actions[0].Status != "running" {
		t.Fatal("completion failure did not preserve committed intent")
	}
	restore()
	reopened, err := Open(h)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Snapshot().Actions[0].Status != "uncertain" {
		t.Fatal("ambiguous delivery not parked")
	}
	if err := reopened.ProcessOne(context.Background(), func(context.Context, Action) (Evaluation, error) {
		t.Fatal("external delivery was resent")
		return Evaluation{}, nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestEvaluationFailureInvalidatesPriorReadyEvidence(t *testing.T) {
	s, h := testStore(t)
	s.db.Tasks["task-1"] = Evaluation{Phase: "ready", Base: strings.Repeat("a", 40), Verified: true, Generation: "g1"}
	if _, err := s.Queue(Action{ID: "recheck", Kind: "evaluate", TaskID: "task-1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.ProcessOne(context.Background(), func(context.Context, Action) (Evaluation, error) {
		return Evaluation{}, fmt.Errorf("evidence source unavailable")
	}); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(h)
	if err != nil {
		t.Fatal(err)
	}
	got := reopened.Snapshot().Tasks["task-1"]
	if got.Phase != "unavailable" || got.Verified || got.Base != strings.Repeat("a", 40) || !strings.Contains(got.Reason, "evidence source unavailable") {
		t.Fatalf("stale evidence remained authoritative: %+v", got)
	}
}

func TestTaskAdmissionCannotEvictUntrackedHistoryToExceedCapacity(t *testing.T) {
	s, h := testStore(t)
	for i := 0; i < maxSessions; i++ {
		s.db.TaskSessions[fmt.Sprintf("task-%d", i+2)] = fmt.Sprintf("codex/session-%d", i)
	}
	s.db.Tasks["untracked"] = Evaluation{Phase: "done"}
	if err := s.save(); err != nil {
		t.Fatal(err)
	}
	if err := s.Accept(event(t, h, "SessionStart", "new", "", time.Now())); err == nil {
		t.Fatal("untracked history let task tracking exceed its bound")
	}
	if len(s.Snapshot().TaskSessions) != maxSessions {
		t.Fatal("failed admission changed task tracking")
	}
}

func TestOversizedStateRollsBackAndRemainsReopenable(t *testing.T) {
	s, h := testStore(t)
	if err := s.Accept(event(t, h, "SessionStart", "s1", "", time.Now())); err != nil {
		t.Fatal(err)
	}
	s.db.Issues = []string{strings.Repeat("x", maxStateBytes)}
	if _, err := s.Queue(Action{ID: "overflow", Kind: "evaluate", TaskID: "task-1"}); err == nil {
		t.Fatal("oversized state was committed")
	}
	reopened, err := Open(h)
	if err != nil || len(reopened.Snapshot().Actions) != 0 || len(s.Snapshot().Issues) != 0 {
		t.Fatalf("oversized write broke the committed state: %v", err)
	}
}

func TestFailedAcceptAndQueueRollBackMemoryAndRetryDurably(t *testing.T) {
	s, h := testStore(t)
	e := event(t, h, "SessionStart", "s1", "", time.Now())
	restore := blockStoreWrites(t, s)
	if err := s.Accept(e); err == nil {
		t.Fatal("write unexpectedly succeeded")
	}
	if len(s.Snapshot().Seen) != 0 || len(s.Snapshot().Sessions) != 0 {
		t.Fatal("failed event remained accepted in memory")
	}
	restore()
	if err := s.Accept(e); err != nil {
		t.Fatal(err)
	}
	restore = blockStoreWrites(t, s)
	a := Action{ID: "retry-action", Kind: "evaluate", TaskID: "task-1"}
	if _, err := s.Queue(a); err == nil {
		t.Fatal("queue write unexpectedly succeeded")
	}
	if len(s.Snapshot().Actions) != 0 {
		t.Fatal("failed action remained in memory")
	}
	restore()
	if _, err := s.Queue(a); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(h)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.Snapshot().Sessions) != 1 || len(reopened.Snapshot().Actions) != 1 {
		t.Fatal("retry did not persist")
	}
}

func TestIngestPreservesStorageAndMetadataFailuresForRetry(t *testing.T) {
	s, h := testStore(t)
	e := event(t, h, "SessionStart", "s1", "", time.Now())
	if err := nativehook.Spool(h.State, e); err != nil {
		t.Fatal(err)
	}
	restore := blockStoreWrites(t, s)
	if _, err := s.Ingest(); err == nil {
		t.Fatal("storage failure was hidden")
	}
	restore()
	meta := filepath.Join(h.State, "task-1.meta")
	if err := os.Rename(meta, meta+".held"); err != nil {
		t.Fatal(err)
	}
	_, _ = s.Ingest()
	files, _ := os.ReadDir(nativehook.SpoolDir(h.State))
	if len(files) != 1 {
		t.Fatal("retryable event was discarded")
	}
	if err := os.Rename(meta+".held", meta); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Ingest(); err != nil {
		t.Fatal(err)
	}
	if len(s.Snapshot().Sessions) != 1 {
		t.Fatal("event did not recover")
	}
}

func TestIngestOrdersWholeBoundedBacklogBeforeBatching(t *testing.T) {
	s, h := testStore(t)
	now := time.Now().Add(-time.Minute)
	start := event(t, h, "SessionStart", "s1", "", now)
	start.ID = strings.Repeat("f", 64)
	if err := nativehook.Spool(h.State, start); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 300; i++ {
		e := event(t, h, "PostToolUse", "s1", "turn", now.Add(time.Duration(i)*time.Millisecond))
		e.ID = fmt.Sprintf("%064x", i)
		if err := nativehook.Spool(h.State, e); err != nil {
			t.Fatal(err)
		}
	}
	for range 3 {
		if _, err := s.Ingest(); err != nil {
			t.Fatal(err)
		}
	}
	view := s.Snapshot()
	if len(view.Seen) != 301 || view.Sessions["codex/s1"].LastEventID != fmt.Sprintf("%064x", 300) || len(view.Issues) != 0 {
		t.Fatalf("backlog lost or reordered: seen=%d session=%+v issues=%v", len(view.Seen), view.Sessions, view.Issues)
	}
}

func event(t *testing.T, h home.Home, name, session, turn string, at time.Time) nativehook.Event {
	t.Helper()
	p, _ := json.Marshal(map[string]string{"hook_event_name": name, "session_id": session, "turn_id": turn, "cwd": filepath.Join(h.Root, "work")})
	e, err := nativehook.Normalize(strings.NewReader(string(p)), nativehook.Context{Harness: "codex", Role: "goblin", TaskID: "task-1", Generation: "g1", Now: at})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestReplayStopDoesNotCompleteTaskAndSurvivesRestart(t *testing.T) {
	s, h := testStore(t)
	now := time.Now().UTC()
	start := event(t, h, "SessionStart", "s1", "", now)
	stop := event(t, h, "Stop", "s1", "t1", now.Add(time.Second))
	for _, e := range []nativehook.Event{start, stop, stop} {
		if err := s.Accept(e); err != nil {
			t.Fatal(err)
		}
	}
	if len(s.Snapshot().Actions) != 1 {
		t.Fatal("duplicate stop queued duplicate action")
	}
	restarted, err := Open(h)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.ProcessOne(context.Background(), func(context.Context, Action) (Evaluation, error) {
		return Evaluation{Phase: "review", Reason: "Review and test evidence missing"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	view := restarted.Snapshot()
	if view.Tasks["task-1"].Phase != "review" || view.Actions[0].Status != "succeeded" {
		t.Fatalf("view = %+v", view)
	}
	if err := restarted.Accept(stop); err != nil {
		t.Fatal(err)
	}
	if len(restarted.Snapshot().Actions) != 1 {
		t.Fatal("replay after restart executed twice")
	}
}

func TestStaleWrongSessionAndGenerationCannotAdvance(t *testing.T) {
	s, h := testStore(t)
	now := time.Now().UTC()
	if err := s.Accept(event(t, h, "SessionStart", "s1", "", now)); err != nil {
		t.Fatal(err)
	}
	active := event(t, h, "UserPromptSubmit", "s1", "t2", now.Add(2*time.Second))
	if err := s.Accept(active); err != nil {
		t.Fatal(err)
	}
	for _, e := range []nativehook.Event{event(t, h, "Stop", "s1", "t1", now.Add(time.Second)), event(t, h, "Stop", "other", "t2", now.Add(3*time.Second))} {
		if err := s.Accept(e); err == nil {
			t.Fatal("accepted stale or unregistered event")
		}
	}
	wrong := event(t, h, "Stop", "s1", "t2", now.Add(3*time.Second))
	wrong.Generation = "old"
	if err := s.Accept(wrong); err == nil {
		t.Fatal("accepted old spawn generation")
	}
	wrong.Generation = "g1"
	wrong.CWD = t.TempDir()
	if err := s.Accept(wrong); err == nil {
		t.Fatal("accepted another worktree")
	}
	if len(s.Snapshot().Actions) != 0 {
		t.Fatal("invalid event queued evaluation")
	}
}

func TestStopContinuationAndSecondStopWithinSameTurn(t *testing.T) {
	s, h := testStore(t)
	now := time.Now()
	var stops []nativehook.Event
	for i, kind := range []string{"SessionStart", "Stop", "PostToolUse", "Stop"} {
		e := event(t, h, kind, "continuation", "same-turn", now.Add(time.Duration(i)*time.Millisecond)) // stall-accepted: each event's timestamp, a millisecond after the one before, which no wait compares with the clock.
		if kind == "Stop" {
			stops = append(stops, e)
		}
		if err := nativehook.Spool(h.State, e); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Ingest(); err != nil {
			t.Fatal(err)
		}
		if kind == "PostToolUse" && s.Snapshot().Sessions["codex/continuation"].Phase != "active" {
			t.Fatal("continuation did not reactivate session")
		}
	}
	if s.Snapshot().Sessions["codex/continuation"].Phase != "settled" {
		t.Fatal("second Stop was lost as a same-turn duplicate")
	}
	if stops[0].ID == stops[1].ID {
		t.Fatal("distinct hook invocations share an identity")
	}
	count := len(s.Snapshot().Actions)
	for _, stop := range stops {
		if err := nativehook.Spool(h.State, stop); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Ingest(); err != nil {
		t.Fatal(err)
	}
	if len(s.Snapshot().Actions) != count {
		t.Fatal("durable replay queued duplicate evaluation")
	}
}

func TestLineageIsPersistedAndCannotCycleOrReparent(t *testing.T) {
	s, h := testStore(t)
	now := time.Now().UTC()
	parent := event(t, h, "SessionStart", "parent", "", now)
	if err := s.Accept(parent); err != nil {
		t.Fatal(err)
	}
	child := event(t, h, "SessionStart", "child", "", now.Add(time.Second))
	child.Role = "subagent"
	child.ParentSessionID = "parent"
	child.ParentHarness = "codex"
	child.Relation = "delegated"
	if err := s.Accept(child); err != nil {
		t.Fatal(err)
	}
	parent.ParentSessionID = "child"
	parent.ParentHarness = "codex"
	parent.Relation = "delegated"
	parent.ID = strings.Repeat("b", 64)
	parent.OccurredAt = now.Add(2 * time.Second)
	if err := s.Accept(parent); err == nil {
		t.Fatal("accepted cycle")
	}
	child.ID = strings.Repeat("a", 64)
	child.ParentSessionID = "different"
	child.OccurredAt = now.Add(3 * time.Second)
	if err := s.Accept(child); err == nil {
		t.Fatal("silently reparented session")
	}
	restarted, err := Open(h)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.Snapshot().Sessions["codex/child"].Parent != "codex/parent" {
		t.Fatal("lost lineage across restart")
	}
}

func TestInterruptedExternalActionIsNeverAutomaticallyResent(t *testing.T) {
	s, h := testStore(t)
	now := time.Now().UTC()
	s.mu.Lock()
	s.db.Actions = append(s.db.Actions, Action{ID: "request-1", Kind: "review", TaskID: "task-1", Generation: "g1", Text: "Review line 4", Status: "running", CreatedAt: now, UpdatedAt: now})
	err := s.save()
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	r, err := Open(h)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	if err := r.ProcessOne(context.Background(), func(context.Context, Action) (Evaluation, error) { calls++; return Evaluation{}, nil }); err != nil {
		t.Fatal(err)
	}
	if calls != 0 || r.Snapshot().Actions[0].Status != "uncertain" {
		t.Fatal("interrupted external action was replayed")
	}
}

func TestObsoleteActionsAreRejectedWithoutReplayingHistoricalRecords(t *testing.T) {
	s, h := testStore(t)
	for _, kind := range []string{"feedback", "cfo_message"} {
		if _, err := s.Queue(Action{ID: "new-" + kind, Kind: kind, TaskID: "task-1", Text: "obsolete"}); err == nil {
			t.Fatalf("new %s action was admitted", kind)
		}
	}
	now := time.Now().UTC()
	s.db.Actions = append(s.db.Actions,
		Action{ID: "historical", Kind: "cfo_message", Text: "recorded", Status: "succeeded", Message: "recorded outcome", CreatedAt: now, UpdatedAt: now},
		Action{ID: "queued-feedback", Kind: "feedback", TaskID: "task-1", Generation: "g1", Text: "obsolete", Status: "queued", CreatedAt: now, UpdatedAt: now},
		Action{ID: "queued-message", Kind: "cfo_message", Text: "obsolete", Status: "queued", CreatedAt: now, UpdatedAt: now},
	)
	if err := s.save(); err != nil {
		t.Fatal(err)
	}
	service := &Service{Store: s}
	if err := s.ProcessOne(context.Background(), service.execute); err != nil {
		t.Fatal(err)
	}
	if err := s.ProcessOne(context.Background(), service.execute); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(h)
	if err != nil {
		t.Fatal(err)
	}
	actions := reopened.Snapshot().Actions
	if actions[0].Status != "succeeded" || actions[0].Message != "recorded outcome" {
		t.Fatalf("historical outcome changed: %+v", actions[0])
	}
	for _, action := range actions[1:] {
		if action.Status != "failed" || !strings.Contains(action.Message, "obsolete") {
			t.Fatalf("queued obsolete action did not fail explicitly: %+v", action)
		}
	}
}

func TestHistoricalObsoleteActionRetriesReturnStoredOutcome(t *testing.T) {
	s, h := testStore(t)
	now := time.Now().UTC()
	historical := []Action{
		{ID: "restarted-feedback", Kind: "feedback", TaskID: "task-1", Generation: "g1", Text: "recorded feedback", Status: "succeeded", Message: "feedback outcome", CreatedAt: now, UpdatedAt: now},
		{ID: "removed-feedback", Kind: "feedback", TaskID: "task-1", Generation: "g1", Text: "removed recipient", Status: "failed", Message: "recorded failure", CreatedAt: now, UpdatedAt: now},
		{ID: "historical-message", Kind: "cfo_message", Generation: "primary-old", Text: "recorded message", Status: "succeeded", Message: "message outcome", CreatedAt: now, UpdatedAt: now},
	}
	s.db.Actions = append(s.db.Actions, historical...)
	if err := s.save(); err != nil {
		t.Fatal(err)
	}
	meta, err := state.ReadTaskMeta(h.State, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	meta.SpawnGen = "g2"
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	got, err := s.Queue(Action{ID: "restarted-feedback", Kind: "feedback", TaskID: "task-1", Text: "recorded feedback"})
	if err != nil || got.Message != "feedback outcome" {
		t.Fatalf("restarted recipient retry lost its outcome: action=%+v err=%v", got, err)
	}
	if err := os.Remove(filepath.Join(h.State, "task-1.meta")); err != nil {
		t.Fatal(err)
	}
	got, err = s.Queue(Action{ID: "removed-feedback", Kind: "feedback", TaskID: "task-1", Text: "removed recipient"})
	if err != nil || got.Message != "recorded failure" {
		t.Fatalf("removed recipient retry lost its outcome: action=%+v err=%v", got, err)
	}
	got, err = s.Queue(Action{ID: "historical-message", Kind: "cfo_message", Generation: "primary-old", Text: "recorded message"})
	if err != nil || got.Message != "message outcome" {
		t.Fatalf("historical CFO retry lost its outcome: action=%+v err=%v", got, err)
	}
	if _, err := s.Queue(Action{ID: "historical-message", Kind: "cfo_message", Generation: "primary-old", Text: "changed"}); err == nil {
		t.Fatal("conflicting historical retry was accepted")
	}
	if len(s.Snapshot().Actions) != len(historical) {
		t.Fatal("historical retry created another action")
	}
	deliveries := 0
	if err := s.ProcessOne(context.Background(), func(context.Context, Action) (Evaluation, error) {
		deliveries++
		return Evaluation{}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if deliveries != 0 {
		t.Fatal("historical retry triggered another delivery")
	}
}

func TestIngestQuarantinesMalformedWithoutLosingValidEvents(t *testing.T) {
	s, h := testStore(t)
	if err := nativehook.Spool(h.State, event(t, h, "SessionStart", "s1", "", time.Now())); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nativehook.SpoolDir(h.State), "broken.event.json"), []byte(`{"schema":`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Ingest(); err != nil {
		t.Fatal(err)
	}
	v := s.Snapshot()
	if len(v.Sessions) != 1 || len(v.Issues) != 1 {
		t.Fatalf("view = %+v", v)
	}
	files, _ := os.ReadDir(nativehook.SpoolDir(h.State))
	if len(files) != 0 {
		t.Fatal("inbox not consumed")
	}
}

// An event that waits for evidence that does not come leaves the inbox, said
// in one issue line: one of a task the fleet never knew after two minutes,
// since a task's record is written before its terminal starts, and any other
// after an hour. One that may still be answered stays to be tried again. On
// 2026-10-27 events of a task that had no record and of retired tasks had
// filled the inbox to its bound, so every goblin's hook found it full and its
// events were lost.
func TestAnEventThatWaitsForEvidenceThatDoesNotComeLeavesTheInbox(t *testing.T) {
	for _, testCase := range []struct {
		name                   string
		task                   string
		isTracked, isUnstarted bool
		age                    time.Duration
		isLeft                 bool
	}{
		{name: "a task never known, five minutes old", task: "nm-daemon-temp", age: 5 * time.Minute},
		{name: "a task never known, thirty seconds old", task: "nm-daemon-temp", age: 30 * time.Second, isLeft: true},
		{name: "a retired task, five minutes old", task: "retired-task", isTracked: true, age: 5 * time.Minute, isLeft: true},
		{name: "a retired task, two hours old", task: "retired-task", isTracked: true, age: 2 * time.Hour},
		{name: "a session with no start, two hours old", task: "task-1", isUnstarted: true, age: 2 * time.Hour},
		{name: "a session with no start, five minutes old", task: "task-1", isUnstarted: true, age: 5 * time.Minute, isLeft: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			s, h := testStore(t)
			if testCase.isTracked {
				s.mu.Lock()
				s.db.Tasks[testCase.task] = Evaluation{Phase: "review", Generation: "g1", At: time.Now().Add(-3 * time.Hour)}
				err := s.save()
				s.mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
			}
			deferred := event(t, h, "UserPromptSubmit", "s1", "turn", time.Now().Add(-testCase.age))
			deferred.TaskID = testCase.task
			if err := nativehook.Spool(h.State, deferred); err != nil {
				t.Fatal(err)
			}

			// Act
			if _, err := s.Ingest(); err != nil {
				t.Fatal(err)
			}

			// Assert
			files, _ := os.ReadDir(nativehook.SpoolDir(h.State))
			issues := s.Snapshot().Issues
			if testCase.isLeft {
				if len(files) != 1 || len(issues) != 0 {
					t.Fatalf("inbox %d events and issues %q, want the event left to be tried again and no issue", len(files), issues)
				}
				return
			}
			if len(files) != 0 || len(issues) != 1 || !strings.Contains(issues[0], "set aside 1 native events that waited for evidence that does not come") {
				t.Fatalf("inbox %d events and issues %q, want the event gone and one issue saying so", len(files), issues)
			}
		})
	}
}

// An event the store's bounds have no room for waits however long that
// takes: the bounds hold events back rather than drop what a goblin did, and
// room comes as sessions end, unlike evidence that does not come.
func TestAnEventThatWaitsForRoomStaysInTheInbox(t *testing.T) {
	// Arrange
	s, h := testStore(t)
	s.mu.Lock()
	for i := range maxSessions {
		id := fmt.Sprintf("codex/busy-%d", i)
		s.db.Sessions[id] = Session{ID: id, NativeID: fmt.Sprintf("busy-%d", i), Harness: "codex", Role: "cfo", Phase: "active", UpdatedAt: time.Now()}
	}
	err := s.save()
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	waiting := event(t, h, "SessionStart", "s1", "", time.Now().Add(-2*time.Hour))
	if err := s.Accept(waiting); err == nil || !strings.Contains(err.Error(), "all retained sessions have recent activity") {
		t.Fatalf("Accept = %v, want it held back for room; the test proves nothing", err)
	}
	if err := nativehook.Spool(h.State, waiting); err != nil {
		t.Fatal(err)
	}

	// Act
	_, err = s.Ingest()

	// Assert
	files, _ := os.ReadDir(nativehook.SpoolDir(h.State))
	if issues := s.Snapshot().Issues; err != nil || len(files) != 1 || len(issues) != 0 {
		t.Fatalf("Ingest = %v, inbox %d events and issues %q, want the event left to wait for room", err, len(files), issues)
	}
}

// However many passes set events aside, one issue line counts them all, so
// a stray spooler cannot push every other diagnostic out of the 20 lines
// the board keeps.
func TestManyPassesThatSetEventsAsideLeaveOneIssueLine(t *testing.T) {
	// Arrange
	s, h := testStore(t)
	const passes = 25

	// Act
	for i := range passes {
		stray := event(t, h, "UserPromptSubmit", fmt.Sprintf("stray-%d", i), "turn", time.Now().Add(-5*time.Minute))
		stray.TaskID = "nm-daemon-temp"
		if err := nativehook.Spool(h.State, stray); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Ingest(); err != nil {
			t.Fatal(err)
		}
	}

	// Assert
	issues := s.Snapshot().Issues
	if len(issues) != 1 || !strings.Contains(issues[0], fmt.Sprintf("set aside %d native events that waited for evidence that does not come", passes)) || !strings.Contains(issues[0], "task nm-daemon-temp has no record") {
		t.Fatalf("issues %q, want one line counting all %d set aside and why the latest was", issues, passes)
	}
}

// waitingForARetiredTask is an event of a task the store tracked and whose
// record is gone, which waits in the inbox to be tried again.
func waitingForARetiredTask(t *testing.T, s *Store, h home.Home) []byte {
	t.Helper()
	s.mu.Lock()
	s.db.Tasks["retired-task"] = Evaluation{Phase: "review", Generation: "g1", At: time.Now().Add(-3 * time.Hour)}
	err := s.save()
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	waiting := event(t, h, "UserPromptSubmit", "s1", "turn", time.Now())
	waiting.TaskID = "retired-task"
	data, err := json.Marshal(waiting)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// inboxFillingWakes is every queued wake for a filling native inbox.
func inboxFillingWakes(t *testing.T, h home.Home) []wake.Record {
	t.Helper()
	pending, err := wake.Pending(h.State)
	if err != nil {
		t.Fatal(err)
	}
	return slices.DeleteFunc(pending, func(record wake.Record) bool {
		return !strings.HasPrefix(record.Detail, "native_inbox_filling:")
	})
}

// An inbox filling toward its bound wakes the CFO once, naming what fills
// it, the task of each event it can read and the rest as unreadable, before
// any goblin's hook finds it full.
func TestAFillingInboxWakesTheCFOOnce(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		isReadable bool
		source     string
	}{
		{name: "events of a task", isReadable: true, source: "retired-task"},
		{name: "unreadable events", source: "unreadable events"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			s, h := testStore(t)
			service := &Service{Store: s, Instance: "same-serve", subscribers: map[chan struct{}]struct{}{}}
			data := []byte("{")
			if testCase.isReadable {
				data = waitingForARetiredTask(t, s, h)
			}
			for i := range inboxFilling {
				if err := os.WriteFile(filepath.Join(nativehook.SpoolDir(h.State), fmt.Sprintf("%04d.event.json", i)), data, 0600); err != nil {
					t.Fatal(err)
				}
			}

			// Act
			for range 2 {
				service.cycle(context.Background(), false)
			}

			// Assert
			wakes := inboxFillingWakes(t, h)
			if len(wakes) != 1 || !strings.Contains(wakes[0].Detail, fmt.Sprintf("the native event inbox holds %d events", inboxFilling)) || !strings.Contains(wakes[0].Detail, fmt.Sprintf("Most come from %s (%d).", testCase.source, inboxFilling)) {
				t.Fatalf("wakes %+v, want one naming the inbox and %s", wakes, testCase.source)
			}
		})
	}
}

// A filling inbox whose wake cannot be raised is still drained, and the
// failure is told; the next cycle repairs that wake rather than raising a
// second one.
func TestAFillingInboxWhoseWakeFailsIsStillDrained(t *testing.T) {
	// Arrange
	s, h := testStore(t)
	service := &Service{Store: s, Instance: "same-serve", subscribers: map[chan struct{}]struct{}{}}
	waiting := waitingForARetiredTask(t, s, h)
	stale := event(t, h, "UserPromptSubmit", "s1", "turn", time.Now().Add(-5*time.Minute))
	stale.TaskID = "nm-daemon-temp"
	if err := nativehook.Spool(h.State, stale); err != nil {
		t.Fatal(err)
	}
	for i := range inboxFilling {
		if err := os.WriteFile(filepath.Join(nativehook.SpoolDir(h.State), fmt.Sprintf("%04d.event.json", i)), waiting, 0600); err != nil {
			t.Fatal(err)
		}
	}
	// An episode marker that cannot be read fails the wake after it is
	// queued, before it is published.
	episode := filepath.Join(h.State, ".watcher-down")
	if err := os.Mkdir(episode, 0700); err != nil {
		t.Fatal(err)
	}

	// Act
	service.cycle(context.Background(), false)
	files, err := os.ReadDir(nativehook.SpoolDir(h.State))
	if err != nil {
		t.Fatal(err)
	}
	reported := service.lastError
	if err := os.Remove(episode); err != nil {
		t.Fatal(err)
	}
	service.cycle(context.Background(), false)

	// Assert
	if len(files) != inboxFilling || !strings.Contains(reported, ".watcher-down") {
		t.Fatalf("inbox %d events and board error %q after the wake failed, want the stale event gone and the failure told", len(files), reported)
	}
	marker, err := wake.ReadEpisode(h.State)
	if err != nil {
		t.Fatal(err)
	}
	if wakes := inboxFillingWakes(t, h); len(wakes) != 1 || !marker.Pending {
		t.Fatalf("wakes %+v and episode %+v, want the one wake published", wakes, marker)
	}
}

// An intake reads an inbox event again only when its file changed: an
// unchanged event deferred again is taken as read before.
func TestAnUnchangedDeferredEventIsNotReadAgain(t *testing.T) {
	// Arrange
	s, h := testStore(t)
	waiting := event(t, h, "UserPromptSubmit", "unstarted", "turn", time.Now())
	if err := nativehook.Spool(h.State, waiting); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(nativehook.SpoolDir(h.State), waiting.ID+".event.json")
	if _, err := s.Ingest(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// The same size and write time with other bytes: a pass that read the
	// file again would find no event in it.
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), int(info.Size())), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}

	// Act
	_, unchangedErr := s.Ingest()
	if err := os.Chtimes(path, info.ModTime().Add(time.Second), info.ModTime().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	_, changedErr := s.Ingest()

	// Assert
	if unchangedErr != nil || changedErr != nil {
		t.Fatal(unchangedErr, changedErr)
	}
	if issues := s.Snapshot().Issues; len(issues) != 1 {
		t.Fatalf("issues %q, want the unchanged file taken as read and the changed one read again as malformed", issues)
	}
}

// An inbox its bound of events that never come in fills is emptied, so a
// goblin's hook can spool its next event again.
func TestAnInboxFullOfStaleDeferredEventsTakesNewEventsAgain(t *testing.T) {
	// Arrange
	s, h := testStore(t)
	stale := event(t, h, "UserPromptSubmit", "s1", "turn", time.Now().Add(-2*time.Hour))
	stale.TaskID = "retired-task"
	data, err := json.Marshal(stale)
	if err != nil {
		t.Fatal(err)
	}
	for i := range nativehook.MaxQueuedEvents {
		if err := os.WriteFile(filepath.Join(nativehook.SpoolDir(h.State), fmt.Sprintf("%04d.event.json", i)), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := nativehook.Spool(h.State, event(t, h, "SessionStart", "s2", "", time.Now())); err == nil {
		t.Fatal("a full inbox took an event; the test proves nothing")
	}

	// Act
	for range nativehook.MaxQueuedEvents/256 + 1 {
		if _, err := s.Ingest(); err != nil {
			t.Fatal(err)
		}
	}

	// Assert
	if err := nativehook.Spool(h.State, event(t, h, "SessionStart", "s2", "", time.Now())); err != nil {
		t.Fatalf("the inbox still refuses a goblin's event: %v", err)
	}
	if _, err := s.Ingest(); err != nil {
		t.Fatal(err)
	}
	if s.Snapshot().Sessions["codex/s2"].Phase != "started" {
		t.Fatalf("sessions %+v, want s2 started", s.Snapshot().Sessions)
	}
}

// A refusal that came before the event changed anything puts nothing back,
// since putting the database back copies every session, task and action: a
// copy for each of 4,096 deferred events took 11 seconds of every cycle on
// 2026-10-27.
func TestARefusalThatChangedNothingCopiesNothing(t *testing.T) {
	// Arrange
	s, h := testStore(t)
	if err := s.Accept(event(t, h, "SessionStart", "s1", "", time.Now().Add(-time.Minute))); err != nil {
		t.Fatal(err)
	}
	unstarted := event(t, h, "UserPromptSubmit", "s2", "turn", time.Now())
	s.mu.Lock()
	before := reflect.ValueOf(s.db.Sessions).Pointer()
	s.mu.Unlock()

	// Act
	err := s.Accept(unstarted)

	// Assert
	if !errors.Is(err, ErrDeferred) {
		t.Fatalf("Accept = %v, want the event deferred", err)
	}
	s.mu.Lock()
	after := reflect.ValueOf(s.db.Sessions).Pointer()
	s.mu.Unlock()
	if after != before {
		t.Error("a deferral that changed nothing copied the database")
	}
}

func TestIngestRecoversConcurrentSpoolCapacityOvershoot(t *testing.T) {
	s, h := testStore(t)
	now := time.Now()
	active := event(t, h, "UserPromptSubmit", "overflow", "turn", now)
	data, err := json.Marshal(active)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < nativehook.MaxQueuedEvents; i++ {
		if err := os.WriteFile(filepath.Join(nativehook.SpoolDir(h.State), fmt.Sprintf("%04d.event.json", i)), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	start := event(t, h, "SessionStart", "overflow", "", now.Add(-time.Second))
	data, err = json.Marshal(start)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nativehook.SpoolDir(h.State), "z.event.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Ingest(); err != nil {
		t.Fatal(err)
	}
	if len(s.Snapshot().Sessions) != 0 {
		t.Fatal("missing start was invented")
	}
	if _, err := s.Ingest(); err != nil {
		t.Fatal(err)
	}
	if s.Snapshot().Sessions["codex/overflow"].Phase != "active" {
		t.Fatal("bounded read window never reached deferred start")
	}
}

// The board sends a message to a goblin naming no generation, and the store
// records the goblin's. A resend after a lost answer names none either, so it
// is the same message, while other words under its ID are refused.
func TestAResentGoblinMessageUnderItsIDReturnsTheFirstAction(t *testing.T) {
	for _, resend := range []struct {
		name    string
		text    string
		isFirst bool
	}{
		{name: "same words", text: "use the staging database", isFirst: true},
		{name: "other words", text: "use the production database"},
	} {
		t.Run(resend.name, func(t *testing.T) {
			// Arrange
			s, _ := testStore(t)
			first, err := s.Queue(Action{ID: "message-1", Kind: "message", TaskID: "task-1", Text: "use the staging database"})
			if err != nil {
				t.Fatal(err)
			}

			// Act
			again, err := s.Queue(Action{ID: "message-1", Kind: "message", TaskID: "task-1", Text: resend.text})

			// Assert
			if first.Generation != "g1" {
				t.Fatalf("the first message recorded generation %q, not the goblin's g1", first.Generation)
			}
			if resend.isFirst {
				if err != nil {
					t.Fatalf("the resend was refused: %v", err)
				}
				if again.ID != first.ID || again.Text != first.Text || again.Generation != first.Generation || again.Status != first.Status {
					t.Fatalf("the resend returned %+v, not the first action %+v", again, first)
				}
			} else if err == nil || !strings.Contains(err.Error(), "already used for another action") {
				t.Fatalf("other words under the same ID were not refused: %v", err)
			}
			if count := len(s.Snapshot().Actions); count != 1 {
				t.Fatalf("the store holds %d actions, not the one message", count)
			}
		})
	}
}

// A new action is never refused because deliveries nobody confirmed fill the
// action bound: with every held action uncertain, a message, a clear and a
// delete each take the place of the oldest one no longer watched for a late
// take, and one still watched stays.
func TestFullActionQueueDropsTheOldestUnconfirmedDelivery(t *testing.T) {
	for _, tc := range []struct {
		name   string
		action Action
	}{
		{"a message to the CFO", Action{ID: "new-action", Kind: "message", Text: "Hold the merge until I say."}},
		{"a clear of a closed question", Action{ID: "new-action", Kind: "question_clear", QuestionID: "question-1", Generation: strings.Repeat("c", 64)}},
		{"a delete of a kept message", Action{ID: "new-action", Kind: "message_withdraw", TaskID: "task-1", Text: "use the staging database"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			store, h := testStore(t)
			if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: "task-1", Generation: "g1", Operation: "pause-1", Action: "pause", Phase: "paused", ResumeNote: overlordWords + "use the staging database"}); err != nil {
				t.Fatal(err)
			}
			start := time.Now().UTC().Add(-time.Hour)
			store.mu.Lock()
			store.db.Questions = append(store.db.Questions, Question{ID: "question-1", Identity: strings.Repeat("c", 64), Text: "Ship it?", Status: "failed", CreatedAt: start})
			for i := range maxActions {
				store.db.Actions = append(store.db.Actions, Action{ID: fmt.Sprintf("unconfirmed-%03d", i), Kind: "cfo_answer", Text: "Yes", Status: "uncertain", CreatedAt: start.Add(time.Duration(i) * time.Second)})
			}
			store.db.Actions[0].Awaiting = &Awaiting{Host: "cfo-host", Since: start, QuietSince: start}
			store.mu.Unlock()

			// Act
			queued, err := store.Queue(tc.action)

			// Assert
			if err != nil {
				t.Fatalf("a new action beside %d unconfirmed ones = %v, want it queued", maxActions, err)
			}
			got := store.Snapshot().Actions
			if len(got) != maxActions || got[0].ID != "unconfirmed-000" || got[len(got)-1].ID != queued.ID {
				t.Fatalf("held %d actions, first %s, last %s; want %d with the watched one kept and the new one last", len(got), got[0].ID, got[len(got)-1].ID, maxActions)
			}
			if slices.ContainsFunc(got, func(a Action) bool { return a.ID == "unconfirmed-001" }) {
				t.Fatal("unconfirmed-001 was kept; want the oldest unconfirmed action no longer watched dropped")
			}
		})
	}
}
