package supervisor

import (
	"context"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestHooklessSnapshotKeepsGateAuthoritySeparateFromTerminalActivity(t *testing.T) {
	for _, test := range []struct {
		name, step, status, wantPhase string
		health                        monitor.Health
	}{
		{"collecting evidence beside an unanswered Rebase decision", "rebase", "awaiting_approval", "blocked", monitor.HealthBusy},
		{"primary submitted an answer but the gate has not moved", "rebase", "awaiting_approval", "blocked", monitor.HealthParked},
		{"accepted Rebase while the worker terminal is busy", "test", "running", "review", monitor.HealthBusy},
		{"accepted Rebase while the worker is at its composer", "test", "running", "review", monitor.HealthParked},
		{"active gate with an idle worker terminal", "test", "running", "review", monitor.HealthIdle},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, home := testStore(t)
			meta, err := state.ReadTaskMeta(home.State, "task-1")
			if err != nil {
				t.Fatal(err)
			}
			meta.Backend, meta.Mode = "native", "no-mistakes"
			meta.HerdrSession, meta.HerdrPaneID = "", ""
			if err := state.WriteTaskMeta(home.State, meta); err != nil {
				t.Fatal(err)
			}
			gitFixture(t, meta.Worktree)
			gate := fakeProgress{value: pipeline.Progress{RunID: "01M6V48WRZBKCFGS974V49206B", Status: "running", Steps: []pipeline.ProgressStep{{Name: test.step, Status: test.status}}}}
			service := &Service{Store: store, Options: Options{Gate: gate}}
			evaluation, err := service.execute(context.Background(), Action{Kind: "evaluate", TaskID: meta.ID, Generation: meta.SpawnGen})
			if err != nil {
				t.Fatal(err)
			}
			evaluation.Generation = meta.SpawnGen
			store.db.Tasks[meta.ID] = evaluation
			if err := store.save(); err != nil {
				t.Fatal(err)
			}
			if err := state.AppendStatus(home.State, meta.ID, "working: gathering gate evidence"); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			observation := monitor.Observation{TaskID: meta.ID, Endpoint: (herdr.Target{}).String(), EndpointVerdict: monitor.ProbePresent, LastObserved: now, Health: test.health, Reason: monitor.None, Digest: "worker-screen", LastSeen: now, LastProgress: now}
			if test.health == monitor.HealthParked {
				observation.Reason = monitor.AwaitingDecision
			}
			if err := monitor.WriteObservation(home.State, observation); err != nil {
				t.Fatal(err)
			}

			snapshot, err := service.Snapshot()

			if err != nil || len(snapshot.Tasks) != 1 {
				t.Fatalf("snapshot failed: %v", err)
			}
			task := snapshot.Tasks[0]
			if task.Phase != test.wantPhase || task.GateStep != test.step || task.Reason != evaluation.Reason {
				t.Errorf("gate authority changed: phase=%s step=%s reason=%s; want %s/%s/%s", task.Phase, task.GateStep, task.Reason, test.wantPhase, test.step, evaluation.Reason)
			}
			if task.Runtime.State != string(test.health) || task.Report != "working" || task.LastReport != "working: gathering gate evidence" {
				t.Errorf("independent activity lost: runtime=%+v report=%s last=%s", task.Runtime, task.Report, task.LastReport)
			}
		})
	}
}
