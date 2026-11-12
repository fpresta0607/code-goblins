package supervisor

import (
	"fmt"

	"github.com/fpresta0607/code-goblins/internal/disk"
	"github.com/fpresta0607/code-goblins/internal/fleetconfig"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
)

// Disk is the free space of the drive the home is on, or of the Dev Drive its
// heavy folders moved to when that has less, beside the fleet's disk floor,
// under which no goblin and no gate test run starts, and the lower mark at
// which the CEO is woken. Every start, wake and AFK decision reads that one
// reading. Drives are what the board's disk meter shows beside the memory
// meter: one line for each drive the fleet writes to, the home's, the Dev
// Drive's and the gate home's, so the Overlord sees the drive the work is on.
type Disk struct {
	disk.Reading
	Floor  uint64       `json:"floor"`
	Wake   uint64       `json:"wake"`
	Drives []disk.Drive `json:"drives"`
}

// MachineDisk reads the home's drives and the floors config/fleet.json sets.
// When the home's drive or the Dev Drive cannot be read it returns the error
// beside the floors and the drives it read, so the board can still show which
// drive it is.
func MachineDisk(h home.Home) (Disk, error) {
	settings, err := fleetconfig.Read(h.Root)
	if err != nil {
		return Disk{}, err
	}
	out := Disk{Floor: fleetconfig.Bytes(settings.DiskFloorGB), Wake: fleetconfig.Bytes(settings.DiskWakeGB), Drives: disk.ReadDrives(fleetFolders(h)...)}
	reading, err := disk.ReadLeast(h.Root, h.DevDrive)
	if err != nil {
		return out, err
	}
	out.Reading = reading
	return out, nil
}

// fleetFolders are the folders the fleet writes to: the home, the Dev Drive
// its heavy folders moved to, and the gate's home. A gate home with no user
// folder to sit in is left out, since there is no drive to name.
func fleetFolders(h home.Home) []disk.Folder {
	folders := []disk.Folder{{Path: h.Root}, {Path: h.DevDrive, IsDevDrive: true}}
	if gate, err := pipeline.DefaultRoot(); err == nil {
		folders = append(folders, disk.Folder{Path: gate, IsOptional: true})
	}
	return folders
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
	return short + "; a start needs the floor free: the janitor's sweep removes what the fleet left behind, and goblins runtime shows what holds the rest"
}

// machineDisk reads the disk through the dispatch's seam when it has one.
func (s *Service) machineDisk() (Disk, error) {
	if s.Options.Dispatch != nil && s.Options.Dispatch.Disk != nil {
		return s.Options.Dispatch.Disk()
	}
	return MachineDisk(s.Store.Home)
}
