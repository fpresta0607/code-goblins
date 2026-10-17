package supervisor

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/nativehook"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// hookNames are each harness's own hook events for the moments a goblin's
// session goes through: its start, taking a prompt, a tool call and the end
// of its turn.
var hookNames = map[string]map[string]string{
	"claude": {"start": "SessionStart", "prompt": "UserPromptSubmit", "tool": "PostToolUse", "settle": "Stop"},
	"codex":  {"start": "SessionStart", "prompt": "UserPromptSubmit", "tool": "PostToolUse", "settle": "Stop"},
	"pi":     {"start": "session_start", "prompt": "agent_start", "tool": "tool_execution_end", "settle": "agent_settled"},
}

// atWorkFixture is a store whose tasks run harness in native terminals the
// monitor last saw busy, each with its own session.
func atWorkFixture(t *testing.T, harness string, ids ...string) (*Store, home.Home) {
	t.Helper()
	store, h := testStore(t)
	now := time.Now().UTC()
	for _, id := range ids {
		meta, err := state.ReadTaskMeta(h.State, "task-1")
		if err != nil {
			t.Fatal(err)
		}
		meta.ID, meta.Harness, meta.Mode, meta.Backend = id, harness, "direct-PR", "native"
		meta.HerdrSession, meta.HerdrPaneID = "", ""
		if err := state.WriteTaskMeta(h.State, meta); err != nil {
			t.Fatal(err)
		}
		observation := monitor.Observation{TaskID: id, Endpoint: (herdr.Target{}).String(), EndpointVerdict: monitor.ProbePresent, LastObserved: now, Health: monitor.HealthBusy, Reason: monitor.None, Digest: "screen", LastSeen: now, LastProgress: now}
		if err := monitor.WriteObservation(h.State, observation); err != nil {
			t.Fatal(err)
		}
	}
	return store, h
}

// harnessHook has task's session report moment at at, in harness's own event.
func harnessHook(t *testing.T, store *Store, h home.Home, harness, task, moment string, at time.Time) {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{"hook_event_name": hookNames[harness][moment], "session_id": task + "-session", "cwd": filepath.Join(h.Root, "work")})
	e, err := nativehook.Normalize(strings.NewReader(string(payload)), nativehook.Context{Harness: harness, Role: "goblin", TaskID: task, Generation: "g1", Now: at})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Accept(e); err != nil {
		t.Fatal(err)
	}
}

// seeTerminal has the monitor read task's terminal at health now.
func seeTerminal(t *testing.T, h home.Home, task string, health monitor.Health) {
	t.Helper()
	now := time.Now().UTC()
	observation := monitor.Observation{TaskID: task, Endpoint: (herdr.Target{}).String(), EndpointVerdict: monitor.ProbePresent, LastObserved: now, Health: health, Reason: monitor.None, Digest: "screen", LastSeen: now, LastProgress: now}
	if err := monitor.WriteObservation(h.State, observation); err != nil {
		t.Fatal(err)
	}
}

func fileReport(t *testing.T, h home.Home, task, line string, at time.Time) {
	t.Helper()
	writeFile(t, state.StatusPath(h.State, task), at.UTC().Format(time.RFC3339)+" "+line+"\n")
}

func snapshotTask(t *testing.T, service *Service, id string) Task {
	t.Helper()
	view, err := service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range view.Tasks {
		if task.ID == id {
			return task
		}
	}
	t.Fatalf("%s missing from the snapshot %+v", id, view.Tasks)
	return Task{}
}

// A wait a goblin reported says what it does until it takes a prompt after
// it: while the turn that prompt started runs, it is at work, and once that
// turn ends its report stands again. Its own tool calls after the report, in
// the turn that filed it, end no wait. Issue #1013.
func TestAWaitEndsWhileTheGoblinWorksOnANewerPrompt(t *testing.T) {
	for _, harness := range []string{"claude", "codex", "pi"} {
		for _, test := range []struct {
			name        string
			after       []string
			wantPhase   string
			wantWaiting string
		}{
			{"at work on a newer prompt", []string{"prompt", "tool"}, "working", ""},
			{"still in the turn that reported it", []string{"tool"}, "waiting", "task-2"},
			{"the newer turn ended", []string{"prompt", "tool", "settle"}, "waiting", "task-2"},
		} {
			t.Run(harness+"/"+test.name, func(t *testing.T) {
				// Arrange
				store, h := atWorkFixture(t, harness, "task-1")
				now := time.Now().UTC()
				harnessHook(t, store, h, harness, "task-1", "start", now.Add(-3*time.Minute))
				harnessHook(t, store, h, harness, "task-1", "prompt", now.Add(-3*time.Minute))
				fileReport(t, h, "task-1", "waiting on task-2: its API contract", now.Add(-time.Minute))
				for i, moment := range test.after {
					harnessHook(t, store, h, harness, "task-1", moment, now.Add(time.Duration(i-len(test.after))*10*time.Second))
				}

				// Act
				got := snapshotTask(t, &Service{Store: store}, "task-1")

				// Assert
				if got.Phase != test.wantPhase || got.WaitingOn != test.wantWaiting {
					t.Fatalf("phase %q waiting on %q (%s), want %q waiting on %q", got.Phase, got.WaitingOn, got.Reason, test.wantPhase, test.wantWaiting)
				}
			})
		}
	}
}

// A delivered goblin that takes a prompt after its done report, such as one
// resumed to look for more work, is at work and in progress while that turn
// runs, whether its gate verified the delivery or its merge completes it; at
// rest it is completed. Issue #1014.
func TestADeliveredGoblinAtWorkAgainIsInProgress(t *testing.T) {
	const pr = "https://github.com/o/r/pull/9"
	for _, harness := range []string{"claude", "codex", "pi"} {
		for _, test := range []struct {
			name           string
			isGateVerified bool
			after          []string
			newerReport    string
			isCompleted    bool
		}{
			{"gate verified, at rest after its report", true, []string{"tool", "settle"}, "", true},
			{"gate verified, at work on a newer prompt", true, []string{"prompt", "tool"}, "", false},
			{"gate verified, at work and saying so", true, []string{"prompt", "tool"}, "working: back at work", false},
			{"gate verified, its newer turn ended", true, []string{"prompt", "tool", "settle"}, "", true},
			{"merged, at rest after its report", false, []string{"tool", "settle"}, "", true},
			{"merged, at work on a newer prompt", false, []string{"prompt", "tool"}, "", false},
		} {
			t.Run(harness+"/"+test.name, func(t *testing.T) {
				// Arrange
				store, h := atWorkFixture(t, harness, "task-1")
				now := time.Now().UTC()
				harnessHook(t, store, h, harness, "task-1", "start", now.Add(-3*time.Minute))
				harnessHook(t, store, h, harness, "task-1", "prompt", now.Add(-3*time.Minute))
				evaluation := Evaluation{Phase: "review", Reason: "Task awaits independent review and delivery evidence", Generation: "g1", At: now.Add(-2 * time.Minute)}
				if test.isGateVerified {
					evaluation.Phase, evaluation.Verified, evaluation.PR = "done", true, pr
				}
				store.mu.Lock()
				store.db.Tasks["task-1"] = evaluation
				err := store.save()
				store.mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				fileReport(t, h, "task-1", "done: PR "+pr, now.Add(-time.Minute))
				for i, moment := range test.after {
					harnessHook(t, store, h, harness, "task-1", moment, now.Add(time.Duration(i-len(test.after))*10*time.Second))
				}
				if test.newerReport != "" {
					if err := state.AppendStatus(h.State, "task-1", test.newerReport); err != nil {
						t.Fatal(err)
					}
				}
				service := &Service{Store: store, history: withMergedPRs(nil, []MergedPR{{PR: pr, Branch: "fix/a", Project: "r", At: now.Unix()}})}

				// Act
				got := snapshotTask(t, service, "task-1")

				// Assert
				if isCompleted := got.Phase == "done" && got.Verified; isCompleted != test.isCompleted {
					t.Fatalf("phase %q verified %v (%s), want completed %v", got.Phase, got.Verified, got.Reason, test.isCompleted)
				}
			})
		}
	}
}

// A goblin switched off Pi onto a harness that reports no native hooks still
// names its Pi session, of an earlier generation, which is no evidence of
// its current one: its card reads its gate and its terminal as any other
// goblin's does.
func TestASessionOfAnEarlierGenerationIsNoEvidence(t *testing.T) {
	// Arrange
	store, h := atWorkFixture(t, "pi", "task-1")
	now := time.Now().UTC()
	harnessHook(t, store, h, "pi", "task-1", "start", now.Add(-time.Hour))
	harnessHook(t, store, h, "pi", "task-1", "prompt", now.Add(-time.Hour))
	meta, err := state.ReadTaskMeta(h.State, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	meta.Harness, meta.SpawnGen = "claude", "g2"
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	store.db.Tasks["task-1"] = Evaluation{Phase: "ready", Verified: true, Reason: "Review, tests, lint, documentation, push and PR checks verified for this HEAD", Generation: "g2", At: now.Add(-time.Minute)}
	err = store.save()
	store.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	seeTerminal(t, h, "task-1", monitor.HealthIdle)

	// Act
	got := snapshotTask(t, &Service{Store: store}, "task-1")

	// Assert
	if got.Phase != "ready" || !got.Verified || got.Runtime.State != string(monitor.HealthIdle) {
		t.Fatalf("phase %q verified %v (%s), runtime %+v, want its gate's ready and its terminal's idle", got.Phase, got.Verified, got.Reason, got.Runtime)
	}
}

// Goblins whose standing waits close a circle each wait on one that waits
// back, so none shows a wait it cannot be, and the CFO is told of the circle
// once. A chain that ends outside the goblins stays a wait. Issue #1013.
func TestAWaitCircleShowsNoWaitAndTellsTheCFOOnce(t *testing.T) {
	// Arrange
	store, h := atWorkFixture(t, "pi", "task-1", "task-2", "task-3")
	now := time.Now().UTC()
	for _, id := range []string{"task-1", "task-2", "task-3"} {
		harnessHook(t, store, h, "pi", id, "start", now.Add(-3*time.Minute))
		harnessHook(t, store, h, "pi", id, "settle", now.Add(-2*time.Minute))
	}
	fileReport(t, h, "task-1", "waiting on task-2: its push", now.Add(-time.Minute))
	fileReport(t, h, "task-2", "waiting on task-1: its review", now.Add(-time.Minute))
	fileReport(t, h, "task-3", "waiting on task-1: its API contract", now.Add(-time.Minute))
	service := &Service{Store: store}

	// Act
	first := service.tellWaitCycles()
	second := service.tellWaitCycles()
	view, err := service.Snapshot()

	// Assert
	if first != nil || second != nil || err != nil {
		t.Fatalf("tell = %v, %v; snapshot: %v", first, second, err)
	}
	for _, task := range view.Tasks {
		switch task.ID {
		case "task-1", "task-2":
			if task.Phase != "blocked" || task.WaitingOn != "" || !strings.Contains(task.Reason, "whose own wait comes back to it") {
				t.Errorf("%s in the circle = phase %q waiting on %q (%s), want blocked on the circle", task.ID, task.Phase, task.WaitingOn, task.Reason)
			}
		case "task-3":
			if task.Phase != "waiting" || task.WaitingOn != "task-1" {
				t.Errorf("task-3 outside the circle = phase %q waiting on %q, want its wait on task-1", task.Phase, task.WaitingOn)
			}
		}
	}
	pending, err := wake.Pending(h.State)
	if err != nil {
		t.Fatal(err)
	}
	var told []string
	for _, record := range pending {
		if strings.HasPrefix(record.Detail, "wait_cycle: ") {
			told = append(told, record.Detail)
		}
	}
	if len(told) != 1 || !strings.Contains(told[0], "task-1") || !strings.Contains(told[0], "task-2") || strings.Contains(told[0], "task-3") {
		t.Fatalf("wait-cycle wakes = %q, want one naming task-1 and task-2 alone", told)
	}
}

// A stopped goblin waits on nothing, whatever its last report said, so its
// old wait closes no circle with a goblin that now waits on it.
func TestAStoppedGoblinsLastWaitClosesNoCircle(t *testing.T) {
	// Arrange
	store, h := atWorkFixture(t, "pi", "task-1", "task-2")
	now := time.Now().UTC()
	for _, id := range []string{"task-1", "task-2"} {
		harnessHook(t, store, h, "pi", id, "start", now.Add(-3*time.Minute))
		harnessHook(t, store, h, "pi", id, "settle", now.Add(-2*time.Minute))
	}
	fileReport(t, h, "task-1", "waiting on task-2: its push", now.Add(-2*time.Minute))
	fileReport(t, h, "task-2", "waiting on task-1: its review", now.Add(-time.Minute))
	if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: "task-1", Generation: "g1", Operation: "stop-1", Action: "stop", Phase: "stopped", Updated: now}); err != nil {
		t.Fatal(err)
	}
	service := &Service{Store: store}

	// Act
	err := service.tellWaitCycles()
	got := snapshotTask(t, service, "task-2")

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if got.Phase != "waiting" || got.WaitingOn != "task-1" {
		t.Errorf("task-2 = phase %q waiting on %q (%s), want its own wait on the stopped goblin", got.Phase, got.WaitingOn, got.Reason)
	}
	pending, err := wake.Pending(h.State)
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(pending, func(record wake.Record) bool { return strings.HasPrefix(record.Detail, "wait_cycle: ") }) {
		t.Errorf("wakes = %+v, want no wait circle told", pending)
	}
}
