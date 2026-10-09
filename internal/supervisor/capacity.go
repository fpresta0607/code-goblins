package supervisor

import (
	"errors"
	"fmt"
)

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

// processorsBusy says why the supervisor starts no goblin by itself at this
// reading, the performance cores being too busy to leave the Overlord's own
// apps room, or "" when they have room or are not read. A Start or Resume he
// clicks does not ask it, and neither does a spawn typed at the command line.
// A reading that fails is reported and holds nothing: memory and disk are the
// floors, and this is a courtesy to his apps.
func (s *Service) processorsBusy() string {
	dispatch := s.Options.Dispatch
	if dispatch == nil || dispatch.Processors == nil {
		return ""
	}
	processors, err := dispatch.Processors()
	if err != nil {
		s.publish(fmt.Errorf("the processors cannot be read, so no start waits on them: %w", err))
		return ""
	}
	return processors.shortfall()
}
