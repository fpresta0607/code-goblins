package supervisor

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/fpresta0607/code-goblins/internal/verify"
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

// sessionMemory is about what a new goblin session takes: the next-start
// mark is the fleet's floor and this.
const sessionMemory = memoryNext - memoryFloor

// SlotHold says why no goblin starts or resumes by itself at the reading
// memory, or "" when one may: a new session takes about 1 GB, and memory and
// commit less that must still meet floor, what the machine's heavy line keeps
// for its next test run as goblins gate turns shows it, so both test slots stay
// usable. The Overlord set this rule on 2026-11-02, by free memory and no
// count of goblins, after goblins started over the next-start mark were
// paused at the memory floor minutes later while heavy steps waited (#1926).
func SlotHold(floor uint64, memory Memory) string {
	free := min(memory.Available, memory.CommitAvailable)
	if free >= floor+sessionMemory {
		return ""
	}
	return fmt.Sprintf("%.1f GB of memory and commit is free; the next test run needs %.1f GB and a new goblin 1 GB", gigabytes(free), gigabytes(floor))
}

// LineRoom is the room the machine's heavy line keeps at a reading of
// memory: Hold, as SlotHold says it, the Floor its next test run needs free,
// which is zero when the line is not read, and how many runs are Waiting in
// it.
type LineRoom struct {
	Hold    string
	Floor   uint64
	Waiting int
}

// MachineHeavyLine is the room the machine's heavy line keeps at the reading
// memory for its next run: the run at its head, or one of the default
// estimate when none waits.
func MachineHeavyLine(memory Memory) (LineRoom, error) {
	dir, err := verify.AdmissionDir()
	if err != nil {
		return LineRoom{}, err
	}
	_, waiting, err := verify.Line(dir)
	if err != nil {
		return LineRoom{}, err
	}
	var estimate uint64
	if len(waiting) > 0 {
		estimate = waiting[0].Estimate
	}
	floor, err := verify.HeadFloor(dir, estimate, 0)
	if err != nil {
		return LineRoom{}, err
	}
	return LineRoom{Hold: SlotHold(floor, memory), Floor: floor, Waiting: len(waiting)}, nil
}

// MachineLineStall says how the machine's heavy line stands still at the
// reading memory: a turn free, runs waiting and memory and commit at the
// floor of the run at its head, which should then take its turn, naming the
// line's lock when a live process has held it long; "" when the line moves
// or waits for memory or a turn. A light run beside the slots holds no turn
// of the line's, so only the slots' holders are counted against it.
func MachineLineStall(memory Memory) (string, error) {
	dir, err := verify.AdmissionDir()
	if err != nil {
		return "", err
	}
	capacity, _, err := verify.AdmissionLimits(dir)
	if err != nil {
		return "", err
	}
	holding, waiting, err := verify.Line(dir)
	holding = slices.DeleteFunc(holding, func(run verify.Standing) bool { return run.IsLight })
	if err != nil || len(holding) >= capacity || len(waiting) == 0 {
		return "", err
	}
	floor, err := verify.HeadFloor(dir, waiting[0].Estimate, 0)
	if err != nil {
		return "", err
	}
	free := min(memory.Available, memory.CommitAvailable)
	if free < floor {
		return "", nil
	}
	stall := fmt.Sprintf("%d of %d turns free and %d in line, %s first, with %.1f GB of memory and commit free against its %.1f GB floor", capacity-len(holding), capacity, len(waiting), waiting[0].Who, gigabytes(free), gigabytes(floor))
	stuck, err := verify.StuckLock(dir, time.Now())
	if err != nil {
		return "", err
	}
	if stuck != nil {
		stall += fmt.Sprintf("; the line's lock (custody-mutation) has been held by pid %d for %s", stuck.PID, time.Since(stuck.Acquired).Round(time.Second))
	}
	return stall, nil
}

// heavyLineRoom is the room the machine's heavy line keeps at this reading,
// whose Hold says why the supervisor starts and resumes no goblin by itself,
// as SlotHold says it, or is "" when it may or the line is not read. A line
// that cannot be read holds nothing, as processors that cannot be read hold
// nothing, and is reported once it stays unread failingPasses readings in a
// row.
func (s *Service) heavyLineRoom(w *fleetWakes, memory Memory) (LineRoom, error) {
	dispatch := s.Options.Dispatch
	if dispatch == nil || dispatch.HeavyLine == nil {
		return LineRoom{}, nil
	}
	room, err := dispatch.HeavyLine(memory)
	if err != nil {
		err = fmt.Errorf("the machine's heavy line cannot be read, so no start waits on it: %w", err)
	}
	return room, w.failing("heavy-line", err)
}
