package supervisor

import (
	"errors"
	"fmt"
	"os"

	"github.com/fpresta0607/code-goblins/internal/fleetconfig"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/state"
)

type FleetCapacity struct {
	Live       int `json:"live"`
	Limit      int `json:"limit"`
	Configured int `json:"configured"`
	Slots      int `json:"slots"`
}

func ReadFleetCapacity(h home.Home, memory Memory) (FleetCapacity, error) {
	settings, err := fleetconfig.Read(h.Root)
	if err != nil {
		return FleetCapacity{}, err
	}
	capacity := FleetCapacity{Configured: settings.MaxLiveGoblins}
	scan, err := state.ScanIDs(h.State)
	if err != nil {
		return capacity, err
	}
	for _, id := range scan.MetaIDs {
		meta, err := state.ReadTaskMeta(h.State, id)
		if err != nil {
			return capacity, err
		}
		if meta.Backend != "native" {
			continue
		}
		record, err := host.ReadRecord(h.State, meta.ID)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return capacity, err
		}
		if host.Running(record) {
			capacity.Live++
		}
	}
	free := min(memory.Available, memory.CommitAvailable)
	resourceSlots := 0
	if free >= memoryFloor {
		resourceSlots = int((free - memoryFloor) / (1 << 30))
	}
	capacity.Limit = min(capacity.Configured, capacity.Live+resourceSlots)
	capacity.Slots = max(0, capacity.Limit-capacity.Live)
	return capacity, nil
}

func CheckLaunch(h home.Home, memory Memory, disk Disk) error {
	if short := memory.shortfall(); short != "" {
		return errors.New(short + "; a start needs 5 GB of memory and commit to keep the 4 GB floor")
	}
	if short := disk.Shortfall(); short != "" {
		return errors.New(diskRefusal(short))
	}
	capacity, err := ReadFleetCapacity(h, memory)
	if err != nil {
		return err
	}
	if capacity.Slots == 0 {
		return fmt.Errorf("live goblin cap reached: %d live, cap %d (configured maximum %d); wait for a goblin to pause or finish", capacity.Live, capacity.Limit, capacity.Configured)
	}
	return nil
}
