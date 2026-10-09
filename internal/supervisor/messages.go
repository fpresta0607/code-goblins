package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
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

// overlordWords open each message where it is delivered.
const overlordWords = "Message from the Overlord, typed on the board: "

// fromOverlord is how a message reads where it is delivered.
func fromOverlord(text string) string {
	return overlordWords + strings.TrimSpace(text)
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
	if !waitsForResume(record) {
		return false, nil
	}
	record.ResumeNote = strings.TrimSpace(record.ResumeNote + "\n" + text)
	return true, state.WriteLifecycle(stateDir, record)
}

// waitsForResume says a goblin's resume note waits for a resume to carry it:
// the goblin is paused, or its resume failed.
func waitsForResume(record state.Lifecycle) bool {
	return record.Phase == "paused" || record.Action == "resume" && record.Phase == "failed"
}

// keptMessages reads a resume note as what came before the board's first
// message in it, such as an answer given while paused, and the board's
// messages after that, each as the Overlord wrote it: a message starts a line
// with overlordWords and runs to the next.
func keptMessages(note string) (before string, messages []string) {
	var lines []string
	for _, line := range strings.Split(note, "\n") {
		text, isMessage := strings.CutPrefix(line, overlordWords)
		switch {
		case isMessage:
			messages = append(messages, text)
		case len(messages) > 0:
			messages[len(messages)-1] += "\n" + line
		default:
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n"), messages
}

// withdrawMessage deletes a message kept for a paused goblin's resume when
// the board asks, so the resume never carries it (the Overlord, 2026-10-09:
// "why is there no delete button for paused queued messages?"). The resume
// note is what the resume carries, so the delete names the message by its
// words in the note, which outlast the message's own action. It is taken out
// here, under the store lock, so every board shows it gone at once, and
// under the goblin's lifecycle lock, so a resume either carried it already
// or starts without it. A message typed into a terminal, carried by a resume
// or never kept is refused.
func (s *Store) withdrawMessage(a Action) (Action, error) {
	if a.TaskID == "" || strings.TrimSpace(a.Text) == "" || a.Generation != "" || a.File != "" || a.Head != "" || a.Revision != "" || a.DiffID != "" || a.Line != 0 || a.EndLine != 0 || a.Side != "" || a.Session != "" || a.EventID != "" || a.QuestionID != "" || a.ReviewID != "" || a.AnswerKind != "" {
		return Action{}, errors.New("a delete names only its goblin and the message's words")
	}
	remove := -1
	if len(s.db.Actions) >= maxActions {
		if remove = slices.IndexFunc(s.db.Actions, func(old Action) bool { return old.Status == "succeeded" || old.Status == "failed" }); remove < 0 {
			return Action{}, errors.New("action queue is full. Resolve pending actions")
		}
	}
	if err := withdrawFromResume(s.Home.State, a.TaskID, a.Text); err != nil {
		return Action{}, err
	}
	if remove >= 0 {
		s.db.Actions = slices.Delete(s.db.Actions, remove, remove+1)
	}
	a.Status, a.Message = "succeeded", "Deleted before its resume."
	a.CreatedAt = time.Now().UTC()
	a.UpdatedAt = a.CreatedAt
	s.db.Actions = append(s.db.Actions, a)
	return a, nil
}

// withdrawFromResume takes the message text out of task's resume note while
// the note still waits for a resume, under the goblin's lifecycle lock.
func withdrawFromResume(stateDir, task, text string) (err error) {
	name := ".lifecycle-" + task + ".lock"
	if _, err := lock.AcquireExclusiveNamed(stateDir, name); err != nil {
		return errors.New(task + "'s resume note is being changed right now, so the message was not deleted. Try again in a moment")
	}
	defer func() { err = errors.Join(err, lock.ReleaseExclusiveNamed(stateDir, name)) }()
	record, err := state.ReadLifecycle(stateDir, task)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	before, messages := keptMessages(record.ResumeNote)
	kept := slices.Index(messages, strings.TrimSpace(text))
	if err != nil || !waitsForResume(record) || kept < 0 {
		return errors.New(task + " has no such message kept for its resume, so it was delivered or never kept")
	}
	note := []string{}
	if before != "" {
		note = append(note, before)
	}
	for _, message := range slices.Delete(messages, kept, kept+1) {
		note = append(note, fromOverlord(message))
	}
	record.ResumeNote = strings.Join(note, "\n")
	return state.WriteLifecycle(stateDir, record)
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
