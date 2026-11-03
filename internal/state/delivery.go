package state

import (
	"path/filepath"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/project"
)

func ReadTaskDelivery(meta TaskMeta) (*project.DeliverySelection, error) {
	if meta.DeliveryHash == "" {
		return nil, nil
	}
	data, err := fsx.ReadFile(filepath.Join(meta.TaskTmp, "delivery.json"))
	if err != nil {
		return nil, err
	}
	selection, err := project.ReadDeliverySelection(data, meta.DeliveryHash, meta.Mode)
	if err != nil {
		return nil, err
	}
	return &selection, nil
}
