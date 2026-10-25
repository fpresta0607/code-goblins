package supervisor

import (
	"fmt"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

func TestCompletedHelperKeepsItsHandoffDespiteIdleRuntime(t *testing.T) {
	for _, harness := range []string{"claude", "codex", "pi"} {
		for _, hasSession := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/session=%t", harness, hasSession), func(t *testing.T) {
				store, h := atWorkFixture(t, harness, "task-1")
				meta, err := state.ReadTaskMeta(h.State, "task-1")
				if err != nil {
					t.Fatal(err)
				}
				meta.Parent, meta.Mode = "parent-1", "local-only"
				if err := state.WriteTaskMeta(h.State, meta); err != nil {
					t.Fatal(err)
				}
				now := time.Now().UTC()
				if hasSession {
					for _, moment := range []string{"start", "prompt", "settle"} {
						harnessHook(t, store, h, harness, meta.ID, moment, now.Add(-2*time.Minute))
					}
				}
				store.db.Tasks[meta.ID] = Evaluation{Generation: meta.SpawnGen, Phase: "review", Reason: "Manual task mode requires its existing review and delivery evidence", At: now.Add(-2 * time.Minute)}
				if err := store.save(); err != nil {
					t.Fatal(err)
				}
				fileReport(t, h, meta.ID, "done: ready for parent-1 to merge", now.Add(-time.Minute))
				seeTerminal(t, h, meta.ID, monitor.HealthIdle)

				got := snapshotTask(t, &Service{Store: store}, meta.ID)

				if got.Phase != "done" || got.Reason != "ready for parent-1 to merge" || got.Report != "done" || got.Activity != "done: ready for parent-1 to merge" {
					t.Fatalf("completed helper = %+v, want its completed parent handoff", got)
				}
				if got.Verified || got.PR != "" || got.WaitingOn != "" || got.Runtime.State != string(monitor.HealthIdle) {
					t.Fatalf("helper completion invented delivery or changed runtime: %+v", got)
				}
			})
		}
	}
}

func TestCompletedHelperKeepsItsHandoffAfterANonReportStatusLine(t *testing.T) {
	for _, line := range []string{"reaped: removed its worktree after the handoff", "superseded: a newer task took its place"} {
		t.Run(line, func(t *testing.T) {
			store, h := atWorkFixture(t, "codex", "task-1")
			meta, err := state.ReadTaskMeta(h.State, "task-1")
			if err != nil {
				t.Fatal(err)
			}
			meta.Parent, meta.Mode = "parent-1", "local-only"
			if err := state.WriteTaskMeta(h.State, meta); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			for _, moment := range []string{"start", "prompt", "settle"} {
				harnessHook(t, store, h, "codex", meta.ID, moment, now.Add(-3*time.Minute))
			}
			store.db.Tasks[meta.ID] = Evaluation{Generation: meta.SpawnGen, Phase: "review", Reason: "Manual task mode requires its existing review and delivery evidence", At: now.Add(-3 * time.Minute)}
			if err := store.save(); err != nil {
				t.Fatal(err)
			}
			fileReport(t, h, meta.ID, "done: ready for parent-1 to merge", now.Add(-2*time.Minute))
			if err := state.AppendStatus(h.State, meta.ID, line); err != nil {
				t.Fatal(err)
			}
			seeTerminal(t, h, meta.ID, monitor.HealthIdle)

			got := snapshotTask(t, &Service{Store: store}, meta.ID)

			if got.Phase != "done" || got.Reason != "ready for parent-1 to merge" {
				t.Fatalf("completed helper = %+v, want its completed parent handoff", got)
			}
			if got.Verified || got.PR != "" || got.WaitingOn != "" || got.Runtime.State != string(monitor.HealthIdle) {
				t.Fatalf("helper completion invented delivery or changed runtime: %+v", got)
			}
		})
	}
}

func TestCompletedHelperPreservesNewWorkAndHeldStates(t *testing.T) {
	for _, scenario := range []struct {
		name, gatePhase, gateStep, wantPhase string
		health                               monitor.Health
		hasNewPrompt, hasNewReport           bool
		isNewGeneration, hasQuestion         bool
		isPaused, isHelper                   bool
	}{
		{name: "busy terminal", health: monitor.HealthBusy, isHelper: true, wantPhase: "working"},
		{name: "new prompt with busy terminal", health: monitor.HealthBusy, hasNewPrompt: true, isHelper: true, wantPhase: "working"},
		{name: "new prompt with idle terminal", health: monitor.HealthIdle, hasNewPrompt: true, isHelper: true, wantPhase: "unavailable"},
		{name: "new work report", health: monitor.HealthIdle, hasNewReport: true, isHelper: true, wantPhase: "idle"},
		{name: "new generation", health: monitor.HealthIdle, isNewGeneration: true, isHelper: true, wantPhase: "idle"},
		{name: "open question", health: monitor.HealthIdle, hasQuestion: true, isHelper: true, wantPhase: "blocked"},
		{name: "paused", health: monitor.HealthIdle, isPaused: true, isHelper: true, wantPhase: "paused"},
		{name: "held gate", health: monitor.HealthIdle, gatePhase: "review", gateStep: "test", isHelper: true, wantPhase: "review"},
		{name: "verified gate", health: monitor.HealthIdle, gatePhase: "ready", isHelper: true, wantPhase: "ready"},
		{name: "ordinary task", health: monitor.HealthIdle, wantPhase: "idle"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			store, h := atWorkFixture(t, "codex", "task-1", "parent-1")
			now := time.Now().UTC()
			meta, err := state.ReadTaskMeta(h.State, "task-1")
			if err != nil {
				t.Fatal(err)
			}
			if scenario.isHelper {
				meta.Parent, meta.Mode = "parent-1", "local-only"
			}
			if scenario.isNewGeneration {
				meta.SpawnGen = fmt.Sprintf("s%d", now.Add(-30*time.Second).UnixNano())
			}
			if err := state.WriteTaskMeta(h.State, meta); err != nil {
				t.Fatal(err)
			}
			if !scenario.isNewGeneration {
				for _, moment := range []string{"start", "prompt", "settle"} {
					harnessHook(t, store, h, "codex", meta.ID, moment, now.Add(-2*time.Minute))
				}
			}
			evaluation := Evaluation{Generation: meta.SpawnGen, Phase: "review", At: now.Add(-2 * time.Minute)}
			if scenario.gatePhase != "" {
				evaluation.Phase, evaluation.GateStep, evaluation.Reason = scenario.gatePhase, scenario.gateStep, "Independent gate evidence"
				evaluation.Verified = scenario.gatePhase == "ready"
			}
			store.db.Tasks[meta.ID] = evaluation
			store.db.Tasks["parent-1"] = Evaluation{Generation: "g1", Phase: "ready", GateStep: "review", Verified: true, Reason: "Parent gate evidence"}
			if err := store.save(); err != nil {
				t.Fatal(err)
			}
			fileReport(t, h, meta.ID, "done: ready for parent-1 to merge", now.Add(-time.Minute))
			if scenario.hasNewPrompt {
				harnessHook(t, store, h, "codex", meta.ID, "prompt", now.Add(-10*time.Second))
			}
			if scenario.hasNewReport {
				if err := state.AppendStatus(h.State, meta.ID, "working: resumed work"); err != nil {
					t.Fatal(err)
				}
			}
			if scenario.hasQuestion {
				if _, err := wake.Append(h.State, "notify", meta.ID, "blocked: Which follow-up?"); err != nil {
					t.Fatal(err)
				}
			}
			if scenario.isPaused {
				if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: meta.ID, Generation: meta.SpawnGen, Operation: "pause-one", Action: "pause", Phase: "paused", Started: now, Updated: now, Reason: "Paused by parent"}); err != nil {
					t.Fatal(err)
				}
			}
			seeTerminal(t, h, meta.ID, scenario.health)
			service := &Service{Store: store}

			got := snapshotTask(t, service, meta.ID)
			parent := snapshotTask(t, service, "parent-1")

			if got.Phase != scenario.wantPhase || got.Verified != evaluation.Verified || got.GateStep != evaluation.GateStep {
				t.Fatalf("helper = %+v, want phase %s with its gate evidence intact", got, scenario.wantPhase)
			}
			if parent.Phase != "ready" || !parent.Verified || parent.GateStep != "review" || parent.Reason != "Parent gate evidence" {
				t.Fatalf("helper changed its parent's gate: %+v", parent)
			}
		})
	}
}
