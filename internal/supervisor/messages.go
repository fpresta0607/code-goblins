package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// The Overlord types a message to a goblin or the CFO on the board whatever
// it is doing (the Overlord, 2026-10-08: "message queued to chief no matter
// whats running"). It is accepted at once as a message action and delivered
// once: typed into a goblin's terminal where its harness takes it, busy or
// not, kept with a paused goblin's resume for its resume prompt to carry, or
// typed into the CFO's terminal once a CFO runs and its input is ready. A
// goblin pausing, resuming or stopping, or whose terminal is not up yet, has
// it wait; one that waits longer than messageWait goes to the CFO with its
// words, so none is lost.

// messageWait is how long a message waits for its goblin's terminal before
// the CFO is given it instead.
const messageWait = 10 * time.Minute

// fromOverlord is how a message reads where it is delivered.
func fromOverlord(text string) string {
	return "Message from the Overlord, typed on the board: " + strings.TrimSpace(text)
}

func (s *Service) deliverMessage(ctx context.Context, a Action) (Evaluation, error) {
	if s.Options.CFO == nil {
		return Evaluation{}, fmt.Errorf("%w: message transport is unavailable", ErrRejected)
	}
	text := fromOverlord(a.Text)
	if a.TaskID == "" {
		identity, isLive := s.Store.liveCFO()
		if !isLive {
			return Evaluation{Reason: "Waits for the CFO."}, ErrDeferred
		}
		return s.Options.CFO.Send(ctx, identity, text)
	}
	stateDir := s.Store.Home.State
	s.starts.Lock()
	isStarting := s.starting == a.TaskID || s.isAsked(a.TaskID)
	s.starts.Unlock()
	meta, err := state.ReadTaskMeta(stateDir, a.TaskID)
	if errors.Is(err, os.ErrNotExist) && !isStarting {
		return Evaluation{}, s.messageToCFO(a, a.TaskID+" was not running and had no Start on its way")
	}
	if err != nil && !isStarting {
		return Evaluation{}, err
	}
	if isStarting {
		return s.waitForGoblin(a)
	}
	record, err := state.ReadLifecycle(stateDir, a.TaskID)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Evaluation{}, err
	}
	isRecorded := err == nil
	switch {
	case isRecorded && record.Phase == "stopped":
		return Evaluation{}, s.messageToCFO(a, a.TaskID+" was stopped before it could take it")
	case isRecorded && (record.Phase == "paused" && record.Generation == meta.SpawnGen || record.Action == "resume" && record.Phase == "failed"):
		isKept, err := keepForResume(stateDir, a.TaskID, text)
		if err != nil {
			return Evaluation{}, err
		}
		if isKept {
			return Evaluation{Reason: "Kept for its resume."}, nil
		}
	case isRecorded && (record.Phase == "pausing" || record.Phase == "resuming" || record.Phase == "stopping"):
	default:
		if NativeTerminalRuns(stateDir, a.TaskID) {
			sent := time.Now().UTC()
			result, err := s.Options.CFO.SendGoblin(ctx, a.TaskID, goblinIdentity(meta), text)
			if errors.Is(err, fleet.ErrQueuedForToolCall) {
				return s.behindGoblinsTurn(a.TaskID, sent, text, "Typed while it was working; it takes the message at its next tool call, or as its current turn ends."), nil
			}
			return result, err
		}
	}
	return s.waitForGoblin(a)
}

// waitForGoblin has a message wait for its goblin, until messageWait has
// passed, when the CFO is given it instead.
func (s *Service) waitForGoblin(a Action) (Evaluation, error) {
	if time.Since(a.CreatedAt) > messageWait {
		return Evaluation{}, s.messageToCFO(a, a.TaskID+" could not take it for "+messageWait.String())
	}
	return Evaluation{Reason: "Waits for " + a.TaskID + "."}, ErrDeferred
}

// keepForResume adds text to what a paused goblin's resume tells it, under
// its lifecycle lock, so a resume that starts meanwhile either carries it or
// starts after it is kept. A lock a resume holds says nothing is kept yet.
func keepForResume(stateDir, task, text string) (isKept bool, err error) {
	name := ".lifecycle-" + task + ".lock"
	if _, err := lock.AcquireExclusiveNamed(stateDir, name); err != nil {
		return false, nil
	}
	defer func() { err = errors.Join(err, lock.ReleaseExclusiveNamed(stateDir, name)) }()
	record, err := state.ReadLifecycle(stateDir, task)
	if err != nil {
		return false, err
	}
	if record.Phase != "paused" && !(record.Action == "resume" && record.Phase == "failed") {
		return false, nil
	}
	record.ResumeNote = strings.TrimSpace(record.ResumeNote + "\n" + text)
	return true, state.WriteLifecycle(stateDir, record)
}

// messageToCFO gives the CFO a message its goblin could not take, with its
// words, and says why the message itself failed.
func (s *Service) messageToCFO(a Action, why string) error {
	detail := fmt.Sprintf("message not delivered: %s, so it is yours to relay or answer. The Overlord wrote: %s", why, a.Text)
	if _, err := wake.Append(s.Store.Home.State, "notify", a.TaskID, bounded(detail, 4000)); err != nil {
		return err
	}
	if _, err := wake.PublishEpisode(s.Store.Home.State); err != nil {
		s.publish(err)
	}
	return fmt.Errorf("%w: %s; the CFO has it", ErrRejected, why)
}
