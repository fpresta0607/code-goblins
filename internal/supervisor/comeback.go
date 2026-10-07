package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// After a restart or sign-out, which end every terminal of the sign-in, the
// supervisor brings the fleet back by itself: first the CFO, in its terminal
// on its own conversation, then each goblin that was working, in its own
// session, one at a time as memory allows. What it brings back is what the
// sign-in before this one left: each terminal started in that sign-in that no
// longer runs, except a goblin that finished, was paused or stopped, or is
// retired, and whatever an earlier comeback had not reached yet.

// Comeback is how the supervisor brings the fleet back after a restart or
// sign-out; without it nothing comes back by itself.
type Comeback struct {
	// SignedIn is when this Windows sign-in began.
	SignedIn func() (time.Time, error)
	// CFO brings the CFO back in its terminal on its own conversation, never
	// on a new one, and says why it did not.
	CFO func(ctx context.Context) error
	// Goblin brings one goblin back in place, in its own session, and tells
	// it the machine restarted.
	Goblin func(ctx context.Context, id string) GoblinComeback
}

// GoblinComeback is what bringing one goblin back did, and Said the reason
// where it waits or did not come back.
type GoblinComeback struct {
	Outcome ComebackOutcome
	Said    string
}

// ComebackOutcome is how one goblin's comeback ended.
type ComebackOutcome int

const (
	// CameBack is a goblin back in its own session.
	CameBack ComebackOutcome = iota + 1
	// AlreadyRuns is a goblin whose terminal runs again, brought back by
	// something else first.
	AlreadyRuns
	// LeftAsItWas is a goblin paused or stopped since, which stays so.
	LeftAsItWas
	// WaitsForRoom is a goblin the machine has no room for yet.
	WaitsForRoom
	// DidNotComeBack is a goblin that could not come back; it stays stopped.
	DidNotComeBack
)

// comeBack takes the comeback's next step at a memory reading at or above
// the floor: the CFO first, then the next goblin once memory and commit read
// at or above the next-start mark twice in a row and a launch has room, one
// at a time. It reports whether anything still waits to come back, while
// which nothing else starts by itself.
func (s *Service) comeBack(now time.Time, memory Memory, w *fleetWakes) (bool, error) {
	comeback := s.Options.Comeback
	if comeback == nil || s.Options.Example {
		return false, nil
	}
	s.comeback.Lock()
	defer s.comeback.Unlock()
	record, isNewPlan, err := s.planComeback(now)
	if err != nil {
		return false, err
	}
	if isNewPlan {
		w.MemoryAbove = 0
		if min(memory.Available, memory.CommitAvailable) >= memoryNext {
			w.MemoryAbove = 1
		}
	}
	next := slices.IndexFunc(record.Goblins, func(entry state.ComebackEntry) bool { return entry.State == state.ComebackWaiting })
	isCFOWaiting := record.CFO != nil && record.CFO.State == state.ComebackWaiting
	if !isCFOWaiting && next < 0 {
		return false, nil
	}
	s.starts.Lock()
	if s.starting != "" || len(s.changing) > 0 || s.isCFOComingBack {
		s.starts.Unlock()
		return true, nil
	}
	if isCFOWaiting {
		s.isCFOComingBack = true
		s.starts.Unlock()
		go s.bringCFOBack(comeback)
		s.notify()
		return true, nil
	}
	if w.MemoryAbove < 2 {
		s.starts.Unlock()
		return true, nil
	}
	id := record.Goblins[next].ID
	if s.changing == nil {
		s.changing = map[string]string{}
		s.changeErrors = map[string]taskChangeError{}
	}
	s.changing[id] = "resume"
	delete(s.changeErrors, id)
	s.starts.Unlock()
	go s.bringGoblinBack(comeback, id, memory)
	s.notify()
	return true, nil
}

// launchRoom says why the machine has no room for one more goblin, as a
// spawn admits one, or nil when it has.
func (s *Service) launchRoom(memory Memory) error {
	if s.Options.Dispatch == nil || s.Options.Dispatch.Disk == nil {
		return errors.New("free disk cannot be read")
	}
	disk, err := s.Options.Dispatch.Disk()
	if err != nil {
		return fmt.Errorf("free disk cannot be read: %w", err)
	}
	return CheckLaunch(s.Store.Home, memory, disk)
}

// planComeback returns the comeback for this sign-in, planned and recorded
// the first time a supervisor asks in it. A home with no record has none from
// the sign-in before, so its first comeback brings nothing back and only
// marks where this sign-in began. The caller holds s.comeback.
func (s *Service) planComeback(now time.Time) (state.Comeback, bool, error) {
	directory := s.Store.Home.State
	if s.signedIn.IsZero() {
		signedIn, err := s.Options.Comeback.SignedIn()
		if err != nil {
			return state.Comeback{}, false, fmt.Errorf("nothing comes back after a restart: %w", err)
		}
		s.signedIn = signedIn
	}
	prior, err := state.ReadComeback(directory)
	switch {
	case err == nil && prior.SignedIn.Equal(s.signedIn):
		return prior, false, nil
	case errors.Is(err, os.ErrNotExist):
		record := state.Comeback{SignedIn: s.signedIn, Planned: now}
		return record, true, state.WriteComeback(directory, record)
	case err != nil:
		return state.Comeback{}, false, fmt.Errorf("nothing comes back after a restart: %w", err)
	}
	record := state.Comeback{SignedIn: s.signedIn, Planned: now}
	// A terminal started in the sign-in before this one, or one an earlier
	// comeback had not reached, that no longer runs.
	ended := func(id string, entry *state.ComebackEntry) bool {
		if entry != nil && entry.State == state.ComebackWaiting {
			return !NativeTerminalRuns(directory, id)
		}
		terminal, err := host.ReadRecord(directory, id)
		return err == nil && !terminal.Started.Before(prior.SignedIn) && terminal.Started.Before(s.signedIn) && !NativeTerminalRuns(directory, id)
	}
	if ended(NativeCFOTerminal, prior.CFO) && !CFORuns(directory) {
		record.CFO = &state.ComebackEntry{ID: NativeCFOTerminal, State: state.ComebackWaiting}
	}
	evaluations := s.Store.Snapshot().Tasks
	for _, meta := range liveTasks(directory) {
		if meta.Backend != "native" || meta.SpawnGen == "" {
			continue
		}
		if evaluation := evaluations[meta.ID]; evaluation.Generation == meta.SpawnGen && (evaluation.Phase == "done" || evaluation.Phase == "merged") {
			continue
		}
		lifecycle, err := state.ReadLifecycle(directory, meta.ID)
		if err == nil && lifecycle.Generation == meta.SpawnGen && lifecycle.Phase != "running" {
			continue
		}
		var earlier *state.ComebackEntry
		if index := slices.IndexFunc(prior.Goblins, func(entry state.ComebackEntry) bool {
			return entry.ID == meta.ID
		}); index >= 0 {
			earlier = &prior.Goblins[index]
		}
		if ended(meta.ID, earlier) {
			record.Goblins = append(record.Goblins, state.ComebackEntry{ID: meta.ID, Generation: meta.SpawnGen, State: state.ComebackWaiting})
		}
	}
	return record, true, state.WriteComeback(directory, record)
}

// bringCFOBack brings the CFO back and records how it went.
func (s *Service) bringCFOBack(comeback *Comeback) {
	err := comeback.CFO(context.Background())
	s.comeback.Lock()
	if record, readErr := state.ReadComeback(s.Store.Home.State); readErr == nil && record.CFO != nil {
		record.CFO.State, record.CFO.Reason, record.CFO.At = state.ComebackBack, "", time.Now().UTC()
		if err != nil {
			record.CFO.State, record.CFO.Reason = state.ComebackStopped, err.Error()
		}
		err = state.WriteComeback(s.Store.Home.State, record)
	} else {
		err = errors.Join(err, readErr)
	}
	s.comeback.Unlock()
	s.starts.Lock()
	s.isCFOComingBack = false
	s.starts.Unlock()
	s.reportComeback(err)
}

// bringGoblinBack brings one goblin back and records how it went. One that
// could not come back stays stopped, with the reason on its card, and wakes
// the CFO; the next goblin's turn comes at the next reading either way.
func (s *Service) bringGoblinBack(comeback *Comeback, id string, memory Memory) {
	directory := s.Store.Home.State
	meta, metaErr := state.ReadTaskMeta(directory, id)
	evaluation := s.Store.Snapshot().Tasks[id]
	lifecycle, lifecycleErr := state.ReadLifecycle(directory, id)
	result := GoblinComeback{Outcome: LeftAsItWas}
	switch {
	case metaErr != nil && !errors.Is(metaErr, os.ErrNotExist):
		result = GoblinComeback{Outcome: DidNotComeBack, Said: "its task record cannot be read: " + metaErr.Error()}
	case metaErr != nil || meta.Backend != "native" || meta.SpawnGen == "":
	case evaluation.Generation == meta.SpawnGen && (evaluation.Phase == "done" || evaluation.Phase == "merged"):
	case lifecycleErr == nil && lifecycle.Generation == meta.SpawnGen && lifecycle.Phase != "running":
	case NativeTerminalRuns(directory, id):
		result.Outcome = AlreadyRuns
	default:
		if err := s.launchRoom(memory); err != nil {
			result = GoblinComeback{Outcome: WaitsForRoom, Said: err.Error()}
		} else {
			result = comeback.Goblin(context.Background(), id)
		}
	}
	s.comeback.Lock()
	record, err := state.ReadComeback(directory)
	if index := slices.IndexFunc(record.Goblins, func(entry state.ComebackEntry) bool { return entry.ID == id }); err == nil && index >= 0 {
		entry := &record.Goblins[index]
		entry.Reason, entry.At = result.Said, time.Now().UTC()
		switch result.Outcome {
		case CameBack, AlreadyRuns:
			entry.State = state.ComebackBack
		case LeftAsItWas:
			record.Goblins = slices.Delete(record.Goblins, index, index+1)
		case WaitsForRoom:
			entry.Reason = "Waits for room: " + result.Said
		default:
			entry.State = state.ComebackStopped
			// A switch that failed after it published a new session leaves
			// the card on that one, which is where the reason shows.
			meta, metaErr := state.ReadTaskMeta(directory, id)
			if metaErr == nil {
				entry.Generation = meta.SpawnGen
			}
			detail := fmt.Sprintf("%s did not come back after the restart: %s. It stays stopped, with this reason on its card", id, result.Said)
			if metaErr == nil && meta.Harness != "" {
				detail += fmt.Sprintf("; once the cause is fixed, cfo switch %s --harness %s brings it back in place", id, meta.Harness)
			}
			err = raiseFleetWake(directory, "check", id, detail)
		}
		err = errors.Join(err, state.WriteComeback(directory, record))
	}
	s.comeback.Unlock()
	s.starts.Lock()
	delete(s.changing, id)
	s.starts.Unlock()
	s.reportComeback(err)
}

// reportComeback keeps what a comeback step met for the next recovery cycle,
// which shows it on the board, and tells the board the comeback moved.
func (s *Service) reportComeback(err error) {
	if err != nil {
		s.mu.Lock()
		s.fleetErr = errors.Join(s.fleetErr, err)
		s.mu.Unlock()
	}
	s.notify()
}

// comebackView is the comeback the board shows: the CFO and the goblins this
// sign-in's comeback brings back, or nil when it brings none.
func (s *Service) comebackView() *state.Comeback {
	if s.Options.Comeback == nil || s.Options.Example {
		return nil
	}
	record, err := state.ReadComeback(s.Store.Home.State)
	if err != nil || record.CFO == nil && len(record.Goblins) == 0 {
		return nil
	}
	return &record
}
