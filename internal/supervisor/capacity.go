package supervisor

import "errors"

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
