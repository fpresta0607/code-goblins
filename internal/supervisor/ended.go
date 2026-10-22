package supervisor

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// A board message to a paused goblin is kept in its lifecycle record's resume
// note for its resume to carry. A stop of that goblin writes a record of its
// own that keeps no note, and a cleanup that retires it leaves the note in a
// record no reading looks at again, since the supervisor reads records only
// for live tasks: so the messages went with the paused record and reached
// neither the goblin nor the CFO. Each now hands them to the CFO with their
// words before it writes over or leaves the record that kept them (issue
// #1317).

// HandOverStoppedNote gives the CFO, with their words, the board messages
// meta's goblin's own record keeps for its resume once a stop ends the goblin
// before that resume runs. A record whose session a later start replaced is
// HandOverReplacedNote's. It takes no lock and writes nothing: its caller
// holds the task's lifecycle lock and writes its own record over this one
// once they are handed on. It says whether each is on the CFO's notify wake,
// so a wake episode that fails to publish after them is said without handing
// them on twice.
func HandOverStoppedNote(stateDir string, meta state.TaskMeta, record state.Lifecycle) (isHanded bool, err error) {
	return handOverEndedNote(stateDir, meta, record, "was stopped")
}

// HandOverRetiredNote gives the CFO, with their words, the board messages
// task meta's lifecycle record keeps for a resume once a cleanup retires the
// task before that resume runs, and takes them out of the note, so nothing
// hands them on again: the goblin's own record's with why the cleanup ends
// them, and those of a record whose session a later start replaced as
// HandOverReplacedNote hands them on. It takes no lock: its caller holds the
// task's lifecycle lock. It says whether the cleanup may go on: not once a
// message could not be put on the CFO's notify wake, which leaves the note as
// it was. The error of one that goes on is a warning: a record that cannot be
// read hands nothing on, since a cleanup, a force archive above all, must
// still retire such a task, and what a hand-off met after the messages went
// to the CFO never hands them on twice.
func HandOverRetiredNote(stateDir string, meta state.TaskMeta) (isRetirable bool, err error) {
	record, err := state.ReadLifecycle(stateDir, meta.ID)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return true, fmt.Errorf("the lifecycle record of %s cannot be read, so no message kept for its resume was handed to the CFO: %w", meta.ID, err)
	}
	if _, messages := keptMessages(record.ResumeNote); !waitsForResume(record) || len(messages) == 0 {
		return true, nil
	}
	var isHanded bool
	if isCurrentRecord(record, meta) {
		isHanded, err = handOverEndedNote(stateDir, meta, record, "was retired by a cleanup")
	} else {
		isHanded, err = HandOverReplacedNote(stateDir, meta, record)
	}
	if !isHanded {
		return false, err
	}
	record.ResumeNote, _ = keptMessages(record.ResumeNote)
	if err = errors.Join(err, state.WriteLifecycle(stateDir, record)); err != nil {
		return true, fmt.Errorf("the messages kept for its resume went to the CFO, but %w", err)
	}
	return true, nil
}

// handOverEndedNote gives the CFO, with their words, the board messages
// meta's goblin's own record keeps for its resume once the goblin ended, as
// ended says, before that resume ran: a paused goblin's, or a failed
// resume's, which carried only the messages kept before it began.
func handOverEndedNote(stateDir string, meta state.TaskMeta, record state.Lifecycle, ended string) (isHanded bool, err error) {
	_, messages := keptMessages(record.ResumeNote)
	if !waitsForResume(record) || !isCurrentRecord(record, meta) || len(messages) == 0 {
		return true, nil
	}
	why := meta.ID + " " + ended + " while it was paused, so the resume the message was kept for never runs"
	if record.Action == "resume" {
		why = meta.ID + " " + ended + " after its resume failed, so that resume never runs again; a message kept before it began at " + record.Started.UTC().Format(time.RFC3339) + " went with it, and one kept after it failed did not"
	}
	for _, message := range messages {
		if err := handToCFO(stateDir, meta.ID, why, message); err != nil {
			return false, err
		}
	}
	_, err = wake.PublishEpisode(stateDir)
	return true, err
}
