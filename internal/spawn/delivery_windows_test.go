package spawn

import (
	"errors"
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
	isReadBeforeLaunch := false
	fixture.request.BeforeLaunch = func() error {
		meta, err := state.ReadTaskMeta(fixture.stateDir, fixture.request.ID)
		if err != nil {
			return err
		}
		frozen, err := state.ReadTaskDelivery(meta)
		if err != nil {
			return err
		}
		if frozen == nil || meta.DeliveryHash == "" || frozen.Mode != selection.Mode {
			return errors.New("delivery selection was not frozen before launch")
		}
		isReadBeforeLaunch = true
		return nil
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
	if err != nil || !isReadBeforeLaunch || meta.DeliveryHash == "" || frozen == nil || frozen.Mode != "no-mistakes" || result.Meta.DeliveryHash != meta.DeliveryHash {
		t.Fatalf("meta=%+v selection=%+v error=%v", meta, frozen, err)
	}
}
