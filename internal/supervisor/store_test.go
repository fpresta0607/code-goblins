package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/nativehook"
	"github.com/fpresta0607/code-goblins/internal/state"
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

func TestCompletionWriteFailureNeverResendsFeedbackAfterRestart(t *testing.T) {
	s, h := testStore(t)
	if _, err := s.Queue(Action{ID: "external", Kind: "feedback", TaskID: "task-1", Text: "hello"}); err != nil {
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
		t.Fatal("feedback was resent")
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
	a := Action{ID: "retry-action", Kind: "feedback", TaskID: "task-1", Text: "test"}
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
	if err := s.Ingest(); err == nil {
		t.Fatal("storage failure was hidden")
	}
	restore()
	meta := filepath.Join(h.State, "task-1.meta")
	if err := os.Rename(meta, meta+".held"); err != nil {
		t.Fatal(err)
	}
	_ = s.Ingest()
	files, _ := os.ReadDir(nativehook.SpoolDir(h.State))
	if len(files) != 1 {
		t.Fatal("retryable event was discarded")
	}
	if err := os.Rename(meta+".held", meta); err != nil {
		t.Fatal(err)
	}
	if err := s.Ingest(); err != nil {
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
		if err := s.Ingest(); err != nil {
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
		e := event(t, h, kind, "continuation", "same-turn", now.Add(time.Duration(i)*time.Millisecond))
		if kind == "Stop" {
			stops = append(stops, e)
		}
		if err := nativehook.Spool(h.State, e); err != nil {
			t.Fatal(err)
		}
		if err := s.Ingest(); err != nil {
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
	if err := s.Ingest(); err != nil {
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
	a, err := s.Queue(Action{ID: "request-1", Kind: "feedback", TaskID: "task-1", Text: "Review line 4"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Queue(a); err != nil {
		t.Fatal(err)
	}
	if len(s.Snapshot().Actions) != 1 {
		t.Fatal("duplicate request")
	}
	s.mu.Lock()
	s.db.Actions[0].Status = "running"
	err = s.save()
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
		t.Fatal("interrupted feedback was replayed")
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
	if err := s.Ingest(); err != nil {
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
	if err := s.Ingest(); err != nil {
		t.Fatal(err)
	}
	if len(s.Snapshot().Sessions) != 0 {
		t.Fatal("missing start was invented")
	}
	if err := s.Ingest(); err != nil {
		t.Fatal(err)
	}
	if s.Snapshot().Sessions["codex/overflow"].Phase != "active" {
		t.Fatal("bounded read window never reached deferred start")
	}
}
