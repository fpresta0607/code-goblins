package spawn

import (
	"errors"
	"os"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestSpawnAdmissionRefusesBeforePublishingATask(t *testing.T) {
	fixture := newQuickFixture(t)
	fixture.service.Admit = func() error { return errors.New("live cap reached") }

	_, err := fixture.service.Spawn(t.Context(), fixture.request)

	_, readErr := state.ReadTaskMeta(fixture.stateDir, fixture.request.ID)
	if err == nil || !errors.Is(readErr, os.ErrNotExist) {
		t.Fatalf("refused spawn published a task: %v, %v", err, readErr)
	}
}
