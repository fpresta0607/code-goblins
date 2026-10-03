package supervisor

import (
	"time"

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// RuntimeEvidence is independent of the last native lifecycle event. Monitor
// observations are already durable; do not copy them into another state file.
type RuntimeEvidence struct {
	State  string    `json:"state"`
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
}

func (s *Service) runtimeEvidence(meta state.TaskMeta, node Session, now time.Time) RuntimeEvidence {
	if record, err := s.lifecycle(meta.ID); err == nil && record.Generation == meta.SpawnGen && record.SuppressesMonitoring(s.Store.Home.State) {
		return RuntimeEvidence{State: record.Phase, Reason: record.Reason, At: record.Updated}
	}
	// The monitor reads a native task from its own terminal, never Herdr.
	source := "Herdr"
	if meta.Backend == "native" {
		source = "Native terminal"
	}
	evidence := RuntimeEvidence{State: "unknown", Reason: "Current " + source + " liveness evidence is unavailable"}
	observation, err := s.observation(meta.ID)
	if err != nil || observation.Endpoint != (herdr.Target{Session: meta.HerdrSession, Pane: meta.HerdrPaneID}).String() || observation.LastObserved.Before(node.UpdatedAt) || (node.Generation != "" && node.Generation != meta.SpawnGen) {
		return evidence
	}
	evidence.At = observation.LastObserved
	if now.Sub(observation.LastObserved) > 2*time.Minute || observation.LastObserved.After(now.Add(time.Minute)) {
		evidence.State = "stale"
		evidence.Reason = source + " liveness observation is stale; last native activity is not proof of a running worker"
		return evidence
	}
	evidence.State = string(observation.Health)
	evidence.Reason = source + " monitor: " + string(observation.Reason)
	if observation.Reason == monitor.None {
		evidence.Reason = source + " reports " + evidence.State
	}
	if observation.Health == monitor.HealthUnknown {
		evidence.State = "unavailable"
	}
	return evidence
}

func (e RuntimeEvidence) working() bool {
	return e.State == string(monitor.HealthBusy) || e.State == string(monitor.HealthActive) || e.State == string(monitor.HealthLaunching)
}
