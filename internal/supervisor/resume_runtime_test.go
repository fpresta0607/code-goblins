package supervisor

import (
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestBoardDropsAnOldLifecycleObservationAfterRelaunch(t *testing.T) {
	for _, health := range []monitor.Health{monitor.HealthPaused, monitor.HealthLaunching, monitor.HealthParked} {
		t.Run(string(health), func(t *testing.T) {
			// Arrange
			handler, h := orderBoard(t)
			meta := state.TaskMeta{ID: "task", Backend: "native", SpawnGen: "replacement"}
			at := time.Now().UTC()
			if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: meta.ID, Generation: "previous", Operation: "pause-1", Action: "pause", Phase: "paused"}); err != nil {
				t.Fatal(err)
			}
			if err := monitor.WriteObservation(h.State, monitor.Observation{TaskID: meta.ID, Endpoint: (herdr.Target{}).String(), EndpointVerdict: monitor.ProbeUnknown, LastObserved: at, Health: health, Reason: monitor.LifecycleOperation}); err != nil {
				t.Fatal(err)
			}

			// Act
			evidence := handler.Service.runtimeEvidence(meta, Session{}, at)

			// Assert
			if evidence.State != "unknown" {
				t.Fatalf("old lifecycle observation survived the relaunch: %+v", evidence)
			}
		})
	}
}
