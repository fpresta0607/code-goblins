package supervisor

import (
	"context"
	"errors"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/nativehook"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
	"testing"
	"time"
)

type runtimeProbe struct{ sample monitor.EndpointSample }

func (p *runtimeProbe) Inspect(context.Context, state.TaskMeta) (monitor.EndpointSample, error) {
	return p.sample, nil
}

func TestReconcileActiveSessionAfterAbruptHarnessLoss(t *testing.T) {
	store, h := testStore(t)
	meta, _ := state.ReadTaskMeta(h.State, "task-1")
	gitFixture(t, meta.Worktree)
	now := time.Now().Add(-time.Second)
	for _, kind := range []string{"SessionStart", "UserPromptSubmit"} {
		if err := store.Accept(event(t, h, kind, "crashed", kind, now)); err != nil {
			t.Fatal(err)
		}
	}
	probe := &runtimeProbe{sample: monitor.EndpointSample{
		Verdict: monitor.ProbePresent, Agent: herdr.AgentAlive, Status: herdr.AgentWorking, Busy: herdr.BusyWorking,
		Endpoint: herdr.Endpoint{Target: herdr.Target{Session: meta.HerdrSession, Pane: meta.HerdrPaneID}, WorkspaceID: meta.HerdrWorkspaceID, TabID: meta.HerdrTabID, PaneID: meta.HerdrPaneID},
		TabLabel: "gb-" + meta.ID, Capture: []byte("fixture is working"),
	}}
	m := monitor.Service{StateDir: h.State, Probe: probe}
	s := &Service{Store: store, Options: Options{Gate: fakeProgress{err: pipeline.ErrNoProgress}, Reconcile: func(ctx context.Context) error { _, err := m.Scan(ctx); return err }}, work: make(chan struct{}, 1), subscribers: map[chan struct{}]struct{}{}}
	s.cycle(context.Background(), true)
	if len(store.Snapshot().Actions) != 0 {
		t.Fatal("live working agent was polled")
	}
	// No Stop or SessionEnd. The next real monitor observation is unavailable.
	probe.sample = monitor.EndpointSample{Verdict: monitor.ProbeMissing}
	if _, err := m.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(h)
	if err != nil {
		t.Fatal(err)
	}
	s.Store = reopened
	if err := s.reconcileTasks(time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	s.process(context.Background())
	if got := reopened.Snapshot().Tasks[meta.ID]; got.Phase != "review" || got.Verified {
		t.Fatalf("lost harness prevented independent evidence evaluation: %+v", got)
	}
	view, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Sessions) != 1 || view.Sessions[0].Runtime.State != "unavailable" || view.Sessions[0].Phase != "active" {
		t.Fatalf("runtime evidence concealed the crash or invented an end event: %+v", view.Sessions)
	}
	if view.Tasks[0].Runtime.State != "unavailable" || view.Tasks[0].Phase == "done" {
		t.Fatalf("loss was treated as completion: %+v", view.Tasks)
	}
	// A delayed observation for another endpoint cannot invalidate a new agent.
	meta.HerdrPaneID = "replacement"
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	view, err = s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if view.Sessions[0].Runtime.State != "unknown" {
		t.Fatal("mismatched runtime observation was reused")
	}
}

type fakeProgress struct {
	value pipeline.Progress
	err   error
}

func (f fakeProgress) Progress(context.Context, string, string) (pipeline.Progress, error) {
	return f.value, f.err
}

func (f fakeProgress) CanSteer(context.Context, string, string, string) error {
	if errors.Is(f.err, pipeline.ErrNoProgress) {
		return nil
	}
	if f.err != nil {
		return f.err
	}
	if f.value.Status == "completed" || f.value.CustodyReturned > 0 {
		return nil
	}
	return errors.New("pipeline retains custody")
}

func TestReconciliationAfterSessionEndAndReadyUntilTerminal(t *testing.T) {
	store, h := testStore(t)
	meta, _ := state.ReadTaskMeta(h.State, "task-1")
	gitFixture(t, meta.Worktree)
	head, err := (Git{}).Head(context.Background(), meta.Worktree)
	if err != nil {
		t.Fatal(err)
	}
	start := event(t, h, "SessionStart", "ended", "", time.Now())
	end := event(t, h, "SessionEnd", "ended", "turn", time.Now().Add(time.Millisecond))
	if err := store.Accept(start); err != nil {
		t.Fatal(err)
	}
	if err := store.Accept(end); err != nil {
		t.Fatal(err)
	}
	s := &Service{Store: store, Options: Options{Gate: fakeProgress{err: pipeline.ErrNoProgress}}, work: make(chan struct{}, 1), subscribers: map[chan struct{}]struct{}{}}
	s.process(context.Background())
	if store.Snapshot().Tasks["task-1"].Phase != "review" {
		t.Fatal("expected pending evidence")
	}
	p := pipeline.Progress{Status: "running", Head: head, ReviewedHead: head, PushedHead: head, PR: "https://github.com/test/repo/pull/1", CIReady: 1}
	for _, name := range []string{"review", "test", "lint", "document", "push", "pr"} {
		p.Steps = append(p.Steps, pipeline.ProgressStep{Name: name, Status: "completed"})
	}
	s.Options.Gate = fakeProgress{value: p}
	s.reconcileTasks(time.Now().Add(time.Minute))
	s.process(context.Background())
	if store.Snapshot().Tasks["task-1"].Phase != "ready" {
		t.Fatal("ended session never became ready")
	}
	p.Steps = append(p.Steps, pipeline.ProgressStep{Name: "ci", Status: "failed"})
	s.Options.Gate = fakeProgress{value: p}
	s.reconcileTasks(time.Now().Add(2 * time.Minute))
	s.process(context.Background())
	if store.Snapshot().Tasks["task-1"].Phase != "blocked" {
		t.Fatal("ready task ignored red checks")
	}
	p.Steps = p.Steps[:len(p.Steps)-1]
	p.PRState = "merged"
	p.TerminalVerified = 1
	s.Options.Gate = fakeProgress{value: p}
	s.reconcileTasks(time.Now().Add(3 * time.Minute))
	s.process(context.Background())
	if store.Snapshot().Tasks["task-1"].Phase != "merged" {
		t.Fatal("merge/branch-head evidence substituted for landed content")
	}
	s.Options.VerifyDelivery = func(context.Context, state.TaskMeta, string, string, string) (string, error) {
		return "verified-main", nil
	}
	s.reconcileTasks(time.Now().Add(4 * time.Minute))
	s.process(context.Background())
	if store.Snapshot().Tasks["task-1"].Phase != "done" {
		t.Fatal("verified landed content did not complete task")
	}
	count := len(store.Snapshot().Actions)
	s.reconcileTasks(time.Now().Add(5 * time.Minute))
	if len(store.Snapshot().Actions) != count {
		t.Fatal("terminal task kept polling")
	}
}

func TestSupervisorProcessesWithoutBrowserAndRestarts(t *testing.T) {
	_, h := testStore(t)
	meta, _ := state.ReadTaskMeta(h.State, "task-1")
	gitFixture(t, meta.Worktree)
	meta.Mode = "local-only"
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	s, err := Start(context.Background(), h, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if s != nil {
			s.Close()
		}
	}()
	if _, err := Start(context.Background(), h, Options{}); err == nil {
		t.Fatal("duplicate controller admitted")
	}
	start := event(t, h, "SessionStart", "background", "", time.Now())
	stop := event(t, h, "Stop", "background", "turn", time.Now().Add(time.Millisecond))
	for _, e := range []nativehook.Event{start, stop} {
		if err := nativehook.Spool(h.State, e); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(10 * time.Second)
	for s.Store.Snapshot().Tasks["task-1"].Phase == "" && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
	}
	if got := s.Store.Snapshot().Tasks["task-1"]; got.Phase != "review" || got.Verified {
		t.Fatalf("Stop cannot prove completion: %+v", got)
	}
	s.Close()
	s = nil
	restarted, err := Start(context.Background(), h, Options{})
	if err != nil {
		t.Fatal(err)
	}
	s = restarted
	recovered := s.Store.Snapshot()
	if len(recovered.Sessions) != 1 {
		t.Fatal("durable session not recovered")
	}
	// Startup may already enqueue its independent reconciliation. Verify the
	// original durable action instead of racing that valid background work.
	for _, action := range recovered.Actions {
		if action.ID == "eval-"+stop.ID && action.Status == "succeeded" {
			return
		}
	}
	t.Fatal("original completed evaluation not recovered")
}

func TestFeedbackRespectsPipelineCustodyAndDiffCoordinates(t *testing.T) {
	store, h := testStore(t)
	meta, _ := state.ReadTaskMeta(h.State, "task-1")
	gitFixture(t, meta.Worktree)
	s := &Service{Store: store, Options: Options{Gate: fakeProgress{value: pipeline.Progress{Status: "running"}}}}
	a := Action{Text: "please fix"}
	if err := s.validateFeedback(context.Background(), meta, a); err == nil {
		t.Fatal("steered task under gate custody")
	}
	s.Options.Gate = fakeProgress{err: errors.New("db unreadable")}
	if err := s.validateFeedback(context.Background(), meta, a); err == nil {
		t.Fatal("unknown custody allowed")
	}
	s.Options.Gate = fakeProgress{err: pipeline.ErrNoProgress}
	if err := s.validateFeedback(context.Background(), meta, a); err != nil {
		t.Fatal(err)
	}
	if diffHasLine("@@ -3,2 +3,2 @@\n-old\n+new\n context\n", 4, "added") || !diffHasLine("@@ -3,2 +3,2 @@\n-old\n+new\n context\n", 3, "removed") {
		t.Fatal("line coordinate validation failed")
	}
}
