package lifecycle

import (
	"context"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// The Overlord, 2026-10-09, on a paused goblin's panel whose Details read
// what its pause could not do: "every time I look at the details it is the
// same text". The panel no longer shows a pause's problems, so its report to
// the CFO is the one place they are told: a pause that took effect while it
// missed its handoff and the rest of its stop still names both.
func TestAPauseThatTookEffectTellsTheCFOWhatItCouldNotDo(t *testing.T) {
	// Arrange: the goblin never writes its handoff, and its terminal ends
	// while the sweep for its other processes runs out of time.
	service, meta := lifecycleFixture(t)
	service.Operations.Stop = func(context.Context, state.TaskMeta, *state.Lifecycle) ([]string, error) {
		return []string{"terminal host pid 7"}, UnfinishedStop{Err: context.DeadlineExceeded}
	}
	service.Operations.Notify = func(record state.Lifecycle) error { return Report(service.StateDir, record) }

	// Act: the board asks for the pause, so nobody reads its command.
	record, err := service.Run(t.Context(), Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "pause-1", Action: "pause", Reason: "memory"})
	pending, pendingErr := wake.Pending(service.StateDir)

	// Assert
	if err != nil || pendingErr != nil || record.Phase != "paused" {
		t.Fatalf("pause = %+v, %v (reading the CFO's queue: %v), want it paused", record, err, pendingErr)
	}
	if len(pending) != 1 || pending[0].Key != meta.ID || !strings.HasPrefix(pending[0].Detail, "lifecycle-paused: ") {
		t.Fatalf("the CFO hears %+v, want one lifecycle-paused report of the task", pending)
	}
	for _, problem := range []string{"no new handoff was saved", "the rest of its stop did not finish: context deadline exceeded"} {
		if !strings.Contains(pending[0].Detail, problem) {
			t.Errorf("the CFO's report %q leaves out %q", pending[0].Detail, problem)
		}
	}
}
