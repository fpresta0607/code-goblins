package supervisor

import (
	"context"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// A goblin already live when names shipped has none in its record. The
// supervisor's recovery pass names it, so the board never mixes names and
// ids.
func TestRecoveryCycleNamesALiveGoblinThatHasNoName(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	s := &Service{Store: store, Instance: "test-instance", subscribers: map[chan struct{}]struct{}{}, work: make(chan struct{}, 1)}

	// Act
	s.cycle(context.Background(), true)

	// Assert
	meta, err := state.ReadTaskMeta(h.State, "task-1")
	if err != nil || meta.GoblinName == "" || meta.GoblinTitle == "" {
		t.Fatalf("record = %+v, %v, want a name and a title", meta, err)
	}
	view, err := s.Snapshot()
	if err != nil || len(view.Tasks) == 0 || view.Tasks[0].GoblinName != meta.GoblinName {
		t.Fatalf("snapshot tasks = %+v, %v, want task-1 named %s", view.Tasks, err, meta.GoblinName)
	}
}
