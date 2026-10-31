package spawn

import (
	"testing"

	"github.com/fpresta0607/code-goblins/internal/project"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestSpawnPersistsDeliveryBeforeTheWorkerStarts(t *testing.T) {
	// Arrange
	fixture := newQuickFixture(t)
	selection, err := project.SelectDelivery(project.Manifest{}, "", "no-mistakes", "")
	if err != nil {
		t.Fatal(err)
	}
	fixture.request.Delivery = &selection
	fixture.request.BeforeLaunch = func() error {
		meta, err := state.ReadTaskMeta(fixture.stateDir, fixture.request.ID)
		if err != nil {
			return err
		}
		_, err = state.ReadTaskDelivery(meta)
		return err
	}

	// Act
	result, err := fixture.service.Spawn(t.Context(), fixture.request)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	meta, err := state.ReadTaskMeta(fixture.stateDir, fixture.request.ID)
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := state.ReadTaskDelivery(meta)
	if err != nil || meta.DeliveryHash == "" || frozen == nil || frozen.Mode != "no-mistakes" || result.Meta.DeliveryHash != meta.DeliveryHash {
		t.Fatalf("meta=%+v selection=%+v error=%v", meta, frozen, err)
	}
}
