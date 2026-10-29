package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/nativehook"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/readcache"
	"github.com/fpresta0607/code-goblins/internal/state"
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

// The board learns which terminal a task runs in, so it opens a native
// terminal's own relay rather than a Herdr view.
func TestSnapshotNamesEachTasksTerminalBackend(t *testing.T) {
	store, h := testStore(t)
	for _, backend := range []string{"native", "herdr"} {
		meta, err := state.ReadTaskMeta(h.State, "task-1")
		if err != nil {
			t.Fatal(err)
		}
		meta.Backend = backend
		if err := state.WriteTaskMeta(h.State, meta); err != nil {
			t.Fatal(err)
		}

		view, err := (&Service{Store: store}).Snapshot()

		if err != nil || len(view.Tasks) == 0 || view.Tasks[0].Backend != backend {
			t.Errorf("the snapshot names backend %+v (%v), want %q", view.Tasks, err, backend)
		}
	}
}

// #1611: the snapshot listed its sessions oldest update first, so a session
// that reported moved to the end of the list, and a board was sent the id of
// every session again, 512 of them, whenever one moved.
func TestASessionThatReportsKeepsItsPlaceInTheSnapshot(t *testing.T) {
	// Arrange: two CFO sessions, the first by id the older.
	store, h := testStore(t)
	s := &Service{Store: store}
	report := func(name, session, turn string, at time.Time) {
		t.Helper()
		payload, err := json.Marshal(map[string]string{"hook_event_name": name, "session_id": session, "turn_id": turn, "cwd": h.Root})
		if err != nil {
			t.Fatal(err)
		}
		event, err := nativehook.Normalize(strings.NewReader(string(payload)), nativehook.Context{Harness: "codex", Role: "cfo", Now: at})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Accept(event); err != nil {
			t.Fatal(err)
		}
	}
	started := time.Now().UTC().Add(-time.Minute)
	report("SessionStart", "session-a", "", started)
	report("SessionStart", "session-b", "", started.Add(time.Second))
	listed := func() []string {
		t.Helper()
		view, err := s.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		var natives []string
		for _, session := range view.Sessions {
			natives = append(natives, session.NativeID)
		}
		return natives
	}
	before := listed()

	// Act: the first reports, so it is now the newest.
	report("UserPromptSubmit", "session-a", "turn-1", started.Add(10*time.Second))

	// Assert
	if after := listed(); !slices.Equal(after, before) || !slices.Equal(after, []string{"session-a", "session-b"}) {
		t.Fatalf("the snapshot lists the sessions %v and then %v, want both times session-a, session-b", before, after)
	}
}

// A live task is named by the short title it was dispatched under, and by its
// ID only when it has none.
func TestSnapshotNamesALiveTaskByItsTitle(t *testing.T) {
	store, h := testStore(t)
	for title, want := range map[string]string{"Install and run on any machine, no setup": "Install and run on any machine, no setup", "": "task-1"} {
		meta, err := state.ReadTaskMeta(h.State, "task-1")
		if err != nil {
			t.Fatal(err)
		}
		meta.Title = title
		if err := state.WriteTaskMeta(h.State, meta); err != nil {
			t.Fatal(err)
		}

		view, err := (&Service{Store: store}).Snapshot()

		if err != nil || len(view.Tasks) == 0 || view.Tasks[0].Title != want {
			t.Errorf("the snapshot names the task %+v (%v), want %q", view.Tasks, err, want)
		}
	}
}

// The board reads a live goblin's name and title from the snapshot's
// goblin_name and goblin_title, and its task id stays the handle.
func TestSnapshotCarriesTheGoblinsNameAndTitleForTheBoard(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	meta, err := state.ReadTaskMeta(h.State, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	meta.GoblinName, meta.GoblinTitle = "Jerry", "Code Designer"
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}

	// Act
	view, err := (&Service{Store: store}).Snapshot()

	// Assert
	if err != nil || len(view.Tasks) == 0 {
		t.Fatalf("snapshot tasks = %+v, %v", view.Tasks, err)
	}
	data, err := json.Marshal(view.Tasks[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"id":"task-1"`, `"goblin_name":"Jerry"`, `"goblin_title":"Code Designer"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("task JSON = %s, want %s", data, want)
		}
	}
}

// A brief parked before dispatch is set aside, never offered as Not started;
// an unparked brief still shows as queued.
func TestSnapshotKeepsParkedBriefsOffTheBoard(t *testing.T) {
	for name, tc := range map[string]struct {
		backlog      string
		isOnTheBoard bool
	}{
		"parked in place": {"## Queued\n- [ ] g7 - Parked in place (hold: set aside) (hold-kind: parked)\n", false},
		"parked section":  {"## Queued\n\n## Parked\n- [ ] g7 - Set aside\n", false},
		"CFO parked form": {"## Queued\n\n## Parked\n- parked **g7** - Set aside by the CFO\n", false},
		"ordinary queued": {"## Queued\n- [ ] g7 - Waiting its turn\n", true},
		"no backlog row":  {"## Queued\n", true},
	} {
		t.Run(name, func(t *testing.T) {
			store, h := testStore(t)
			if err := os.MkdirAll(filepath.Join(h.Data, "g7"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(h.Data, "g7", "brief.md"), []byte("brief"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(h.Data, "backlog.md"), []byte(tc.backlog), 0o644); err != nil {
				t.Fatal(err)
			}

			view, err := (&Service{Store: store}).Snapshot()

			if err != nil {
				t.Fatal(err)
			}
			isOnTheBoard := slices.ContainsFunc(view.Tasks, func(task Task) bool { return task.ID == "g7" && task.Phase == "queued" })
			if isOnTheBoard != tc.isOnTheBoard {
				t.Errorf("g7 queued on the board = %v, want %v: %+v", isOnTheBoard, tc.isOnTheBoard, view.Tasks)
			}
		})
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
	end := event(t, h, "SessionEnd", "ended", "turn", time.Now().Add(time.Millisecond)) // stall-accepted: the event's timestamp, a millisecond after its start, which no wait compares with the clock.
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
	deliveryChecks := 0
	s.Options.VerifyDelivery = func(context.Context, state.TaskMeta, string, string, string) (string, error) {
		deliveryChecks++
		return "verified-main", nil
	}
	s.Options.Gate = fakeProgress{value: p}
	s.reconcileTasks(time.Now().Add(3 * time.Minute))
	s.process(context.Background())
	merged := store.Snapshot().Tasks["task-1"]
	if merged.Phase != "merged" || merged.Verified || deliveryChecks != 0 {
		t.Fatalf("merged task bypassed terminal verification: %+v checks=%d", merged, deliveryChecks)
	}
	p.TerminalVerified = 1
	s.Options.Gate = fakeProgress{value: p}
	s.reconcileTasks(time.Now().Add(4 * time.Minute))
	s.process(context.Background())
	if store.Snapshot().Tasks["task-1"].Phase != "done" || deliveryChecks != 1 {
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
	stop := event(t, h, "Stop", "background", "turn", time.Now().Add(time.Millisecond)) // stall-accepted: the event's timestamp, a millisecond after its start, which no wait compares with the clock.
	for _, e := range []nativehook.Event{start, stop} {
		if err := nativehook.Spool(h.State, e); err != nil {
			t.Fatal(err)
		}
	}
	// The startup reconciliation may evaluate the task before its native
	// events arrive, so wait for the Stop event's own evaluation.
	if evaluation := waitForAction(t, s, "eval-"+stop.ID); evaluation.Status != "succeeded" {
		t.Fatalf("the Stop event's evaluation = %+v, want it succeeded", evaluation)
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

// waitForAction waits until the running service has finished the action id,
// which may not be queued yet, and returns it. An evaluation reads the task
// with git, which on a loaded machine takes longer than any guess, so the
// wait is on the service's own notices. It gives up only after every action
// already waiting and this one could each have used all of actionTimeout.
func waitForAction(t *testing.T, s *Service, id string) Action {
	t.Helper()
	changed, unsubscribe := s.subscribe()
	defer unsubscribe()
	ahead := 0
	for _, action := range s.Store.Snapshot().Actions {
		if action.Status == "queued" || action.Status == "running" {
			ahead++
		}
	}
	expired := time.After(time.Duration(ahead+1) * actionTimeout)
	for {
		for _, action := range s.Store.Snapshot().Actions {
			if action.ID == id && action.Status != "queued" && action.Status != "running" {
				return action
			}
		}
		select {
		case <-changed:
		case <-expired:
			t.Fatalf("the service did not finish action %s within %s: %+v", id, time.Duration(ahead+1)*actionTimeout, s.Store.Snapshot().Actions)
		}
	}
}

func TestTerminalControlRespectsPipelineCustody(t *testing.T) {
	store, h := testStore(t)
	meta, _ := state.ReadTaskMeta(h.State, "task-1")
	gitFixture(t, meta.Worktree)
	s := &Service{Store: store, Options: Options{Gate: fakeProgress{value: pipeline.Progress{Status: "running"}}}}
	if err := s.validateTerminalControl(context.Background(), meta); err == nil {
		t.Fatal("steered task under gate custody")
	}
	s.Options.Gate = fakeProgress{err: errors.New("db unreadable")}
	if err := s.validateTerminalControl(context.Background(), meta); err == nil {
		t.Fatal("unknown custody allowed")
	}
	s.Options.Gate = fakeProgress{err: pipeline.ErrNoProgress}
	if err := s.validateTerminalControl(context.Background(), meta); err != nil {
		t.Fatal(err)
	}
}

// A worktree on a detached HEAD, as a new task's starts, has no branch for a
// gate's custody to be read on, so typing into its terminal stays held. The
// board says so and names the commit, where it said Git's symbolic-ref
// failure and woke the CFO each time (#1015).
func TestBoardTerminalOnADetachedHeadHoldsTypingAndSaysWhy(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	meta, _ := state.ReadTaskMeta(h.State, "task-1")
	gitFixture(t, meta.Worktree)
	s := &Service{Store: store, Options: Options{Gate: fakeProgress{err: pipeline.ErrNoProgress}}}
	onBranch, onBranchErr := s.Git.Detached(context.Background(), meta.Worktree)
	git := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = meta.Worktree
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v", out, err)
		}
		return strings.TrimSpace(string(out))
	}
	git("checkout", "-q", "--detach", "HEAD")
	commit := git("rev-parse", "HEAD")[:12]
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	// Act
	detached, detachedErr := s.Git.Detached(context.Background(), meta.Worktree)
	controlErr := s.boardTerminalControl(context.Background(), meta)
	cancelledErr := s.boardTerminalControl(cancelled, meta)
	details, detailErr := s.workspaceDetail(context.Background(), meta.ID, meta.SpawnGen)

	// Assert
	if onBranch || onBranchErr != nil {
		t.Errorf("Detached on a branch = %v, %v; want false", onBranch, onBranchErr)
	}
	if !detached || detachedErr != nil {
		t.Errorf("Detached on a detached HEAD = %v, %v; want true", detached, detachedErr)
	}
	if controlErr == nil || !strings.HasPrefix(controlErr.Error(), "HEAD is detached at "+commit) {
		t.Errorf("board terminal control = %v, want typing refused on HEAD is detached at %s", controlErr, commit)
	}
	if cancelledErr == nil || strings.HasPrefix(cancelledErr.Error(), "HEAD is detached") {
		t.Errorf("cancelled board terminal control = %v, want the cancellation", cancelledErr)
	}
	if detailErr != nil || details.Branch != "detached at "+commit || slices.Contains(details.Notes, "Branch information is unavailable.") {
		t.Errorf("workspace = %+v, %v; want detached at %s with no unavailable note", details, detailErr, commit)
	}
}

func TestEvaluationOfDetachedHeadWaitsForBranchAndRetries(t *testing.T) {
	store, h := testStore(t)
	meta, err := state.ReadTaskMeta(h.State, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	gitFixture(t, meta.Worktree)
	checkout := exec.Command("git", "checkout", "-q", "--detach", "HEAD")
	checkout.Dir = meta.Worktree
	if output, err := checkout.CombinedOutput(); err != nil {
		t.Fatalf("detach fixture: %s: %v", output, err)
	}
	service := &Service{Store: store, Options: Options{Gate: fakeProgress{err: pipeline.ErrNoProgress}}}
	action := Action{Kind: "evaluate", TaskID: meta.ID, Generation: meta.SpawnGen}

	detached, err := service.execute(context.Background(), action)
	if err != nil || detached.Phase != "review" || detached.Verified || !strings.Contains(detached.Reason, "detached") {
		t.Fatalf("detached evaluation = %+v, %v; want unverified review explaining detached HEAD", detached, err)
	}

	checkout = exec.Command("git", "switch", "-q", "-c", "fix/evaluation-fixture")
	checkout.Dir = meta.Worktree
	if output, err := checkout.CombinedOutput(); err != nil {
		t.Fatalf("create fixture branch: %s: %v", output, err)
	}
	retried, err := service.execute(context.Background(), action)
	if err != nil || retried.Phase != "review" || retried.Verified || retried.Reason != pipeline.ErrNoProgress.Error() {
		t.Fatalf("branch evaluation after retry = %+v, %v; want gate evidence read", retried, err)
	}
}

func TestEvaluationReportsGitFailureWithoutExposingStderrAndRetries(t *testing.T) {
	store, h := testStore(t)
	meta, err := state.ReadTaskMeta(h.State, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	gitFixture(t, meta.Worktree)
	isFailing := true
	service := &Service{Store: store, Options: Options{Gate: fakeProgress{err: pipeline.ErrNoProgress}}, Git: Git{Reads: readcache.New()}}
	service.Git.stream = func(ctx context.Context, request execx.Request, stdout, stderr io.Writer) (int, error) {
		if isFailing && slices.Equal(request.Args[len(request.Args)-4:], []string{"symbolic-ref", "--quiet", "--short", "HEAD"}) {
			_, _ = io.WriteString(stderr, "fatal: permission denied\nhttps://user:password@example.invalid/repo?token=ghp_12345678901234567890")
			return 128, nil
		}
		return (execx.OSRunner{}).Stream(ctx, request, stdout, stderr)
	}
	action := Action{Kind: "evaluate", TaskID: meta.ID, Generation: meta.SpawnGen}

	failed, err := service.execute(context.Background(), action)
	if err == nil || !strings.Contains(err.Error(), "exit status 128") || !strings.Contains(err.Error(), "permission denied") || strings.Contains(err.Error(), "password") || strings.Contains(err.Error(), "ghp_") || strings.Contains(err.Error(), "example.invalid") || failed.Verified {
		t.Fatalf("failed evaluation = %+v, %v; want actionable sanitized Git failure", failed, err)
	}
	isFailing = false
	retried, err := service.execute(context.Background(), action)
	if err != nil || retried.Verified || retried.Reason != pipeline.ErrNoProgress.Error() {
		t.Fatalf("retried evaluation = %+v, %v; want fresh gate evidence read", retried, err)
	}
}

func TestEvaluationPreservesBaseGitFailures(t *testing.T) {
	for _, test := range []struct {
		name        string
		retained    bool
		failingArgs []string
	}{
		{"retained base validation", true, []string{"merge-base", "--is-ancestor"}},
		{"base lookup", false, []string{"merge-base", "HEAD"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, h := testStore(t)
			meta, err := state.ReadTaskMeta(h.State, "task-1")
			if err != nil {
				t.Fatal(err)
			}
			gitFixture(t, meta.Worktree)
			checkout := exec.Command("git", "checkout", "-q", "--detach", "HEAD")
			checkout.Dir = meta.Worktree
			if output, err := checkout.CombinedOutput(); err != nil {
				t.Fatalf("detach fixture: %s: %v", output, err)
			}
			if test.retained {
				base := gitOutput(t, meta.Worktree, "rev-parse", "HEAD~1")
				store.mu.Lock()
				store.db.Tasks[meta.ID] = Evaluation{Base: base, Generation: meta.SpawnGen}
				err := store.save()
				store.mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
			}
			isFailing := true
			service := &Service{Store: store, Options: Options{Gate: fakeProgress{err: pipeline.ErrNoProgress}}}
			service.Git.stream = func(ctx context.Context, request execx.Request, stdout, stderr io.Writer) (int, error) {
				if isFailing && len(request.Args) >= 5 && slices.Equal(request.Args[3:5], test.failingArgs) {
					return 128, nil
				}
				return (execx.OSRunner{}).Stream(ctx, request, stdout, stderr)
			}
			action := Action{Kind: "evaluate", TaskID: meta.ID, Generation: meta.SpawnGen}
			failed, err := service.execute(context.Background(), action)
			if err == nil || !strings.Contains(err.Error(), "Git merge-base failed: exit status 128") || failed.Verified {
				t.Fatalf("failed evaluation = %+v, %v; want base Git failure", failed, err)
			}
			isFailing = false
			retried, err := service.execute(context.Background(), action)
			if err != nil || retried.Base == "" || !strings.Contains(retried.Reason, "detached") {
				t.Fatalf("retried evaluation = %+v, %v; want retained base and detached review", retried, err)
			}
		})
	}
}

func TestEvaluationFailureKeepsOnlyTheCurrentGenerationBase(t *testing.T) {
	for _, test := range []struct {
		name            string
		priorGeneration string
		keepBase        bool
	}{
		{"current generation", "g1", true},
		{"previous generation", "g0", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, h := testStore(t)
			meta, err := state.ReadTaskMeta(h.State, "task-1")
			if err != nil {
				t.Fatal(err)
			}
			gitFixture(t, meta.Worktree)
			oldBase := gitOutput(t, meta.Worktree, "rev-parse", "HEAD")
			newBase := gitOutput(t, meta.Worktree, "rev-parse", "HEAD~1")
			store.mu.Lock()
			store.db.Tasks[meta.ID] = Evaluation{Base: oldBase, Generation: test.priorGeneration}
			store.mu.Unlock()
			isFailing := true
			service := &Service{Store: store, Options: Options{Gate: fakeProgress{err: pipeline.ErrNoProgress}}}
			service.Git.stream = func(ctx context.Context, request execx.Request, stdout, stderr io.Writer) (int, error) {
				if isFailing && request.Args[3] == "merge-base" {
					return 128, nil
				}
				return (execx.OSRunner{}).Stream(ctx, request, stdout, stderr)
			}
			if _, err := store.Queue(Action{ID: "failed-evaluation", Kind: "evaluate", TaskID: meta.ID}); err != nil {
				t.Fatal(err)
			}
			if err := store.ProcessOne(context.Background(), service.execute); err != nil {
				t.Fatal(err)
			}
			failed := store.Snapshot().Tasks[meta.ID]
			wantFailedBase := ""
			if test.keepBase {
				wantFailedBase = oldBase
			}
			if failed.Phase != "unavailable" || failed.Generation != meta.SpawnGen || failed.Base != wantFailedBase {
				t.Fatalf("failed evaluation = %+v; want base %q for current generation", failed, wantFailedBase)
			}
			isFailing = false
			if _, err := store.Queue(Action{ID: "retry-evaluation", Kind: "evaluate", TaskID: meta.ID}); err != nil {
				t.Fatal(err)
			}
			if err := store.ProcessOne(context.Background(), service.execute); err != nil {
				t.Fatal(err)
			}
			wantRetryBase := newBase
			if test.keepBase {
				wantRetryBase = oldBase
			}
			retried := store.Snapshot().Tasks[meta.ID]
			if retried.Base != wantRetryBase || retried.Generation != meta.SpawnGen || retried.Phase != "review" {
				t.Fatalf("retried evaluation = %+v; want base %q", retried, wantRetryBase)
			}
		})
	}
}

func TestEvaluationHandlesUnavailableBases(t *testing.T) {
	for _, test := range []struct {
		name           string
		unrelatedBase  bool
		missingRef     bool
		wantGitFailure bool
	}{
		{"unset origin", false, false, false},
		{"unrelated retained base", true, false, false},
		{"origin points to missing commit", false, true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, h := testStore(t)
			meta, err := state.ReadTaskMeta(h.State, "task-1")
			if err != nil {
				t.Fatal(err)
			}
			gitFixture(t, meta.Worktree)
			if test.unrelatedBase {
				base := gitOutput(t, meta.Worktree, "commit-tree", "HEAD^{tree}", "-m", "Unrelated root")
				store.mu.Lock()
				store.db.Tasks[meta.ID] = Evaluation{Base: base, Generation: meta.SpawnGen}
				err := store.save()
				store.mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
			}
			args := []string{"symbolic-ref", "--delete", "refs/remotes/origin/HEAD"}
			if test.missingRef {
				args = []string{"symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/missing"}
			}
			for _, command := range [][]string{args, {"checkout", "-q", "--detach", "HEAD"}} {
				git := exec.Command("git", command...)
				git.Dir = meta.Worktree
				if output, err := git.CombinedOutput(); err != nil {
					t.Fatalf("git %v: %s: %v", command, output, err)
				}
			}
			result, err := (&Service{Store: store}).execute(context.Background(), Action{Kind: "evaluate", TaskID: meta.ID, Generation: meta.SpawnGen})
			if test.wantGitFailure {
				if err == nil || !strings.Contains(err.Error(), "Git merge-base failed: exit status 128") {
					t.Fatalf("evaluation = %+v, %v; want lookup failure", result, err)
				}
			} else if err != nil || result.Base != "" || !strings.Contains(result.Reason, "detached") {
				t.Fatalf("evaluation = %+v, %v; want detached review without base", result, err)
			}
		})
	}
}

func TestEvaluationReportsMissingWorktree(t *testing.T) {
	store, h := testStore(t)
	meta, err := state.ReadTaskMeta(h.State, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	meta.Worktree = filepath.Join(h.Root, "missing-worktree")
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	result, err := (&Service{Store: store}).execute(context.Background(), Action{Kind: "evaluate", TaskID: meta.ID, Generation: meta.SpawnGen})
	if err == nil || result.Verified || !strings.Contains(err.Error(), "Git rev-parse") {
		t.Fatalf("missing worktree evaluation = %+v, %v; want Git error", result, err)
	}
}

func TestSnapshotKeepsEvaluationWithinCurrentGeneration(t *testing.T) {
	tests := []struct {
		name                 string
		evaluationGeneration string
		linked               bool
		sessionGeneration    string
		phase                string
		wantPhase            string
		wantVerified         bool
	}{
		{name: "settled current session rejects completed prior generation", evaluationGeneration: "g1", linked: true, phase: "settled", wantPhase: "review"},
		{name: "ended current session rejects completed prior generation", evaluationGeneration: "g1", linked: true, phase: "ended", wantPhase: "review"},
		// A session of an earlier generation is no evidence of this one, such
		// as the Pi session of a goblin since switched to a harness that has
		// reported none yet, so the current generation's own evaluation stands.
		{name: "prior generation session leaves the matching evaluation standing", evaluationGeneration: "g2", linked: true, sessionGeneration: "g1", phase: "settled", wantPhase: "done", wantVerified: true},
		{name: "unlinked task rejects completed prior generation", evaluationGeneration: "g1", wantPhase: "unknown"},
		{name: "matching generation retains completed evaluation", evaluationGeneration: "g2", linked: true, phase: "settled", wantPhase: "done", wantVerified: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, h := testStore(t)
			meta, err := state.ReadTaskMeta(h.State, "task-1")
			if err != nil {
				t.Fatal(err)
			}
			meta.SpawnGen = "g2"
			if err := state.WriteTaskMeta(h.State, meta); err != nil {
				t.Fatal(err)
			}
			store.db.Tasks[meta.ID] = Evaluation{Phase: "done", Reason: "prior delivery", Generation: tt.evaluationGeneration, Verified: true, At: time.Now().Add(-time.Minute)}
			if tt.linked {
				sessionGeneration := meta.SpawnGen
				if tt.sessionGeneration != "" {
					sessionGeneration = tt.sessionGeneration
				}
				store.db.TaskSessions[meta.ID] = "codex/current"
				store.db.Sessions["codex/current"] = Session{ID: "codex/current", TaskID: meta.ID, Generation: sessionGeneration, Role: "goblin", Phase: tt.phase, UpdatedAt: time.Now()}
			}
			if err := store.save(); err != nil {
				t.Fatal(err)
			}
			view, err := (&Service{Store: store}).Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			if len(view.Tasks) != 1 || view.Tasks[0].Phase != tt.wantPhase || view.Tasks[0].Verified != tt.wantVerified {
				t.Fatalf("snapshot reused the wrong generation: %+v", view.Tasks)
			}
		})
	}
}

// A task in its gate names the step the gate is on, so the board can say
// In review gate: tests, including a step parked for a decision.
func TestEvaluationNamesTheGateStep(t *testing.T) {
	store, h := testStore(t)
	meta, _ := state.ReadTaskMeta(h.State, "task-1")
	gitFixture(t, meta.Worktree)
	for name, c := range map[string]struct {
		steps []pipeline.ProgressStep
		want  string
	}{
		"test running":  {[]pipeline.ProgressStep{{Name: "review", Status: "completed"}, {Name: "test", Status: "running"}, {Name: "lint", Status: "pending"}}, "test"},
		"review parked": {[]pipeline.ProgressStep{{Name: "review", Status: "awaiting_approval"}, {Name: "test", Status: "pending"}}, "review"},
		"not started":   {[]pipeline.ProgressStep{{Name: "review", Status: "pending"}}, ""},
		"all done":      {[]pipeline.ProgressStep{{Name: "review", Status: "completed"}, {Name: "ci", Status: "skipped"}}, ""},
	} {
		s := &Service{Store: store, Options: Options{Gate: fakeProgress{value: pipeline.Progress{Status: "running", Steps: c.steps}}}}
		got, err := s.execute(context.Background(), Action{Kind: "evaluate", TaskID: meta.ID, Generation: meta.SpawnGen})
		if err != nil || got.GateStep != c.want {
			t.Errorf("%s: gate step = %q (%v), want %q", name, got.GateStep, err, c.want)
		}
	}
}
