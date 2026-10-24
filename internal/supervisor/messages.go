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
	"github.com/fpresta0607/code-goblins/internal/pipeline"
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
// it wait; one that waits longer than messageWait, or is kept for a resume
// whose session a later start replaced, goes to the CFO with its words, so
// none is lost.

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
		// A start under way reads the branch too, so a message held by a gate
		// takes the same path across a restart as it does while it runs.
		return s.waitForGoblin(a, isGateUnclearNow(ctx, s, meta))
	}
	record, err := state.ReadLifecycle(stateDir, a.TaskID)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Evaluation{}, err
	}
	isRecorded := err == nil && isCurrentRecord(record, meta)
	switch {
	case isRecorded && record.Phase == "stopped":
		return Evaluation{}, s.messageToCFO(a, a.TaskID+" was stopped before it could take it")
	case isRecorded && waitsForResume(record):
		isKept, err := keepForResume(stateDir, a.TaskID, text)
		if err != nil {
			return Evaluation{}, err
		}
		if isKept {
			return Evaluation{Reason: "Kept for its resume."}, nil
		}
	}
	// A goblin whose gate owns its branch takes no typing, so a message for it
	// is kept as a durable queued action and delivered once custody is released.
	// It never expires and is never handed to the CFO, because nothing is lost by
	// waiting for the gate (the Overlord, 2026-10-23). This holds whatever the
	// goblin is doing, a pause, resume or stop under way included, since a
	// transition reads no custody of its own.
	switch held, _ := gateCustody(ctx, s, meta); held {
	case gateHeld:
		return Evaluation{Reason: "Queued for " + a.TaskID + ". Its gate holds the branch, so it is delivered once the gate lets go, however long that takes."}, ErrDeferred
	case gateUnknown:
		// Custody nobody could read types nothing, so nothing can break a gate.
		// The message waits in the queue like one behind a gate and is delivered
		// the moment custody reads clear, and each attempt reads it again, so
		// nothing is lost while the check is failing. The words he reads are
		// plain, and no child output reaches them.
		return Evaluation{Reason: "Queued for " + a.TaskID + ". Delivery waits until its gate custody can be read."}, ErrDeferred
	}
	switch {
	case isRecorded && (record.Phase == "pausing" || record.Phase == "resuming" || record.Phase == "stopping"):
		// A transition under way has no terminal that takes typing now, and its
		// custody read clear above, so the ordinary wait applies.
		return s.waitForGoblin(a, false)
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
	// A hold returned above, so nothing here is waiting behind one: a goblin
	// whose terminal cannot take the message keeps the ordinary handoff.
	return s.waitForGoblin(a, false)
}

// gateState is what a check of a goblin's gate said: the branch is its
// goblin's, the gate owns it, or nobody could tell.
type gateState int

const (
	gateClear gateState = iota
	gateHeld
	gateUnknown
)

// gateCustody reads whether a goblin's gate owns its branch. Only the two
// refusals a gate makes for its own custody are a hold: a branch that cannot be
// read, a gate this home cannot ask, or any other failure is custody nobody
// could read, which is not a hold and is never reported as one.
func gateCustody(ctx context.Context, s *Service, meta state.TaskMeta) (gateState, error) {
	if meta.Mode != "no-mistakes" {
		return gateClear, nil
	}
	if s.Options.Gate == nil {
		return gateUnknown, errors.New("this home has no gate to ask")
	}
	branch, err := s.Git.Branch(ctx, meta.Worktree)
	if err != nil {
		return gateUnknown, err
	}
	switch err := s.Options.Gate.CanSteer(ctx, meta.Project, meta.Worktree, branch); {
	case err == nil:
		return gateClear, nil
	case errors.Is(err, pipeline.ErrCustodyHeld), errors.Is(err, pipeline.ErrCustodyNotReturned):
		return gateHeld, nil
	default:
		return gateUnknown, err
	}
}

// isGateUnclearNow says a goblin's gate custody is not known to be clear at
// this moment: it owns the branch, or it could not be read. A message waits
// past the ordinary limit in both, since neither can type it, and a start
// under way that read custody as unreadable would otherwise expire a message
// the gate is holding.
func isGateUnclearNow(ctx context.Context, s *Service, meta state.TaskMeta) bool {
	held, _ := gateCustody(ctx, s, meta)
	return held != gateClear
}

// waitForGoblin has a message wait for its goblin, until messageWait has
// passed, when the CFO is given it instead.
func (s *Service) waitForGoblin(a Action, isGated bool) (Evaluation, error) {
	if !isGated && time.Since(a.CreatedAt) > messageWait {
		return Evaluation{}, s.messageToCFO(a, a.TaskID+" could not take it for "+messageWait.String())
	}
	return Evaluation{Reason: "Waits for " + a.TaskID + "."}, ErrDeferred
}

// keepForResume adds text to what a paused goblin's resume, or a failed
// resume of its own, tells it, under its lifecycle lock, so a resume that
// starts meanwhile either carries it or starts after it is kept. A lock a
// resume holds says nothing is kept yet, and so does a record an earlier
// session left.
func keepForResume(stateDir, task, text string) (isKept bool, err error) {
	name := ".lifecycle-" + task + ".lock"
	if _, err := lock.AcquireExclusiveNamed(stateDir, name); err != nil {
		return false, nil
	}
	defer func() { err = errors.Join(err, lock.ReleaseExclusiveNamed(stateDir, name)) }()
	meta, err := state.ReadTaskMeta(stateDir, task)
	if err != nil {
		return false, err
	}
	record, err := state.ReadLifecycle(stateDir, task)
	if err != nil {
		return false, err
	}
	if !waitsForResume(record) || !isCurrentRecord(record, meta) {
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

// handOverReplacedResumes gives the CFO the board messages each task's
// lifecycle record keeps for a resume whose session a later start replaced.
func handOverReplacedResumes(stateDir string) error {
	var problems error
	for _, meta := range liveTasks(stateDir) {
		problems = errors.Join(problems, handOverReplacedResume(stateDir, meta.ID))
	}
	return problems
}

// handOverReplacedResume takes the board messages task id's record keeps for
// a resume whose session a later start replaced out of its note once
// HandOverReplacedNote has given them to the CFO. The start publishes its
// session without the lifecycle lock, so a message is kept for the replaced
// session both before the start and while it runs. It works under the task's
// lifecycle lock, so a message kept meanwhile is either handed on here or
// never kept. A lock held by a pause or stop under way leaves the record to
// it, which hands the messages on before it writes its own record, and one
// held by a resume, refused for a replaced session, leaves them for the next
// reading. The messages are handed on before they leave the note, so a
// hand-off that fails leaves them for the next reading, and what the note
// held before them, such as an answer given while paused, stays.
func handOverReplacedResume(stateDir, id string) (err error) {
	if _, _, isReplaced, err := replacedResume(stateDir, id); err != nil || !isReplaced {
		return err
	}
	name := state.LifecycleLockName(id)
	if _, err := lock.AcquireExclusiveNamed(stateDir, name); err != nil {
		return nil
	}
	defer func() { err = errors.Join(err, lock.ReleaseExclusiveNamed(stateDir, name)) }()
	record, meta, isReplaced, err := replacedResume(stateDir, id)
	if err != nil || !isReplaced {
		return err
	}
	isHanded, err := HandOverReplacedNote(stateDir, meta, record)
	if !isHanded {
		return err
	}
	record.ResumeNote, _ = keptMessages(record.ResumeNote)
	return errors.Join(err, state.WriteLifecycle(stateDir, record))
}

// HandOverReplacedNote gives the CFO, with their words, the board messages
// record keeps for a resume once a later start, such as a cfo switch, has
// replaced the session that resume was for, since no resume of that record
// runs again. It takes no lock and writes nothing: its caller holds the
// task's lifecycle lock and takes the messages out of the note, or writes its
// own record over it, once they are handed on. It says whether each is on the
// CFO's notify wake, so a wake episode that fails to publish after them is
// said without handing them on twice.
func HandOverReplacedNote(stateDir string, meta state.TaskMeta, record state.Lifecycle) (isHanded bool, err error) {
	messages := replacedMessages(record, meta)
	if len(messages) == 0 {
		return true, nil
	}
	why := meta.ID + "'s session was replaced by a later start while it was paused, so the resume the message was kept for never runs"
	if record.Action == "resume" {
		why = meta.ID + "'s session was replaced by a later start after its resume failed, so that resume never runs again; a message kept before it began at " + record.Started.UTC().Format(time.RFC3339) + " went with it, and one kept after it failed did not"
	}
	for _, message := range messages {
		if err := handToCFO(stateDir, meta.ID, why, message); err != nil {
			return false, err
		}
	}
	_, err = wake.PublishEpisode(stateDir)
	return true, err
}

// replacedResume reads task id's lifecycle record and task metadata, and
// says whether the record keeps board messages for a resume a later start's
// session replaced.
func replacedResume(stateDir, id string) (state.Lifecycle, state.TaskMeta, bool, error) {
	var meta state.TaskMeta
	record, err := state.ReadLifecycle(stateDir, id)
	if errors.Is(err, os.ErrNotExist) {
		return record, meta, false, nil
	}
	if err != nil || !waitsForResume(record) {
		return record, meta, false, err
	}
	meta, err = state.ReadTaskMeta(stateDir, id)
	return record, meta, err == nil && len(replacedMessages(record, meta)) > 0, err
}

// replacedMessages are the board messages record keeps for a resume a later
// start's session replaced: record waits for a resume, as a paused goblin's
// or a failed resume's does, and is neither meta's session's nor its
// interrupted resume.
func replacedMessages(record state.Lifecycle, meta state.TaskMeta) []string {
	if !waitsForResume(record) || isCurrentRecord(record, meta) {
		return nil
	}
	_, messages := keptMessages(record.ResumeNote)
	return messages
}

// isCurrentRecord says record is meta's goblin's own: meta's session's, or
// the task's interrupted resume. A record an earlier session left says
// nothing of the goblin, and decides nothing of where its message goes: a
// failed resume whose session a later start replaced, as a cfo switch run to
// rescue the goblin does, can never be resumed again.
func isCurrentRecord(record state.Lifecycle, meta state.TaskMeta) bool {
	return record.Generation == meta.SpawnGen || record.IsInterruptedResume(meta)
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
	// A message waiting for a gate to let go is withdrawn from the queue it
	// waits in, so no later attempt delivers it.
	if at := slices.IndexFunc(s.db.Actions, func(queued Action) bool {
		return queued.Kind == "message" && queued.Status == "queued" && queued.TaskID == a.TaskID && strings.TrimSpace(queued.Text) == strings.TrimSpace(a.Text)
	}); at >= 0 {
		delete(s.deferredUntil, s.db.Actions[at].ID)
		s.db.Actions = slices.Delete(s.db.Actions, at, at+1)
		now := time.Now().UTC()
		a.Status, a.Message, a.CreatedAt, a.UpdatedAt = "succeeded", "Deleted before its gate let go.", now, now
		s.db.Actions = append(s.db.Actions, a)
		return a, nil
	}
	remove := -1
	if len(s.db.Actions) >= maxActions {
		if remove = slices.IndexFunc(s.db.Actions, isClosedHistory); remove < 0 {
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
	if err := handToCFO(s.Store.Home.State, a.TaskID, why, a.Text); err != nil {
		return err
	}
	if _, err := wake.PublishEpisode(s.Store.Home.State); err != nil {
		s.publish(err)
	}
	return fmt.Errorf("%w: %s; the CFO has it", ErrRejected, why)
}

// handToCFO puts a message to task that its goblin could not take on the
// task's notify wake, with its words and why it was not delivered.
func handToCFO(stateDir, task, why, text string) error {
	detail := fmt.Sprintf("message not delivered: %s, so it is yours to relay or answer. The Overlord wrote: %s", why, text)
	_, err := wake.Append(stateDir, "notify", task, bounded(detail, 4000))
	return err
}
