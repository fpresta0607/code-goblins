package supervisor

import (
	"errors"
	"os"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// FleetCapacity is how many goblins free memory carries: the goblins whose
// terminals run, the limit memory sets on them, and the slots left under it.
// Memory is the only limit: each slot is the room one more goblin needs, the
// stretch from the floor to the next-start mark, above the floor in the
// tighter of free memory and free commit.
type FleetCapacity struct {
	Live  int `json:"live"`
	Limit int `json:"limit"`
	Slots int `json:"slots"`
}

func ReadFleetCapacity(h home.Home, memory Memory) (FleetCapacity, error) {
	var capacity FleetCapacity
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
	if free := min(memory.Available, memory.CommitAvailable); free >= memoryFloor {
		capacity.Slots = int((free - memoryFloor) / (memoryNext - memoryFloor))
	}
	capacity.Limit = capacity.Live + capacity.Slots
	return capacity, nil
}

// CheckLaunch refuses a start, a resume or a spawn while memory or commit is
// under the next-start mark or the disk under its floor. No count of goblins
// refuses one: memory alone says whether another fits.
func CheckLaunch(memory Memory, disk Disk) error {
	if short := memory.shortfall(); short != "" {
		return errors.New(short + "; a start needs 5 GB of memory and commit to keep the 4 GB floor")
	}
	if short := disk.Shortfall(); short != "" {
		return errors.New(diskRefusal(short))
	}
	return nil
}
