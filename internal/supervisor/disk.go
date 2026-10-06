package supervisor

import (
	"fmt"

	"github.com/fpresta0607/code-goblins/internal/disk"
	"github.com/fpresta0607/code-goblins/internal/fleetconfig"
	"github.com/fpresta0607/code-goblins/internal/home"
)

// Disk is the free space of the drive the home is on, beside the fleet's disk
// floor, under which no goblin and no gate test run starts, and the lower mark
// at which the CFO is woken. The board shows it beside the memory meter.
type Disk struct {
	disk.Reading
	Floor uint64 `json:"floor"`
	Wake  uint64 `json:"wake"`
}

// MachineDisk reads the home's drive and the floors config/fleet.json sets.
func MachineDisk(h home.Home) (Disk, error) {
	settings, err := fleetconfig.Read(h.Root)
	if err != nil {
		return Disk{}, err
	}
	reading, err := disk.Read(h.Root)
	if err != nil {
		return Disk{}, err
	}
	return Disk{Reading: reading, Floor: fleetconfig.Bytes(settings.DiskFloorGB), Wake: fleetconfig.Bytes(settings.DiskWakeGB)}, nil
}

// Shortfall says why a start does not fit on the disk, with the free space and
// the floor, or "" when it does.
func (d Disk) Shortfall() string {
	if d.Free >= d.Floor {
		return ""
	}
	return fmt.Sprintf("free disk on %s is %.1f GB, under the %.0f GB disk floor", d.Drive, disk.GB(d.Free), disk.GB(d.Floor))
}

// diskRefusal is the whole refusal a start under the floor gets.
func diskRefusal(short string) string {
	return short + "; a start needs the floor free: the janitor's sweep removes what the fleet left behind, and cfo runtime shows what holds the rest"
}

// machineDisk reads the disk through the dispatch's seam when it has one.
func (s *Service) machineDisk() (Disk, error) {
	if s.Options.Dispatch != nil && s.Options.Dispatch.Disk != nil {
		return s.Options.Dispatch.Disk()
	}
	return MachineDisk(s.Store.Home)
}
