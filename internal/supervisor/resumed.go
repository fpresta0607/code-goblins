package supervisor

import (
	"errors"
	"os"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// A resume is recorded failed when its relaunched harness was not seen
// working on its instruction within the resume's window, though the goblin
// may take it after: on 2026-10-20 Teddy's resumed Enter did not take on a
// loaded machine, the CFO pressed it by hand, and Teddy worked on in the
// resumed session while the board drew "Resume failed" for hours and kept the
// Overlord's messages for a resume that never came. A goblin that reports
// from the session its resume started, after the resume was recorded failed,
// has resumed, so its record is finished as running, and each board message
// kept for that resume goes to the CFO, since nothing types it now. A Resume
// that finds the terminal that resume started already up relaunches nothing,
// so it hands each such message to the CFO the same way before it writes its
// own record over the failed one (issue #1281).

// finishTakenResumes finishes as running each failed resume whose goblin has
// reported from the session the resume started.
func finishTakenResumes(stateDir string) error {
	var problems error
	for _, meta := range liveTasks(stateDir) {
		problems = errors.Join(problems, finishTakenResume(stateDir, meta.ID))
	}
	return problems
}

// finishTakenResume finishes task id's failed resume as running once its
// goblin has reported from the session the resume started, under the task's
// lifecycle lock. A lock held by a pause, resume or stop under way leaves the
// record to it, and the next reading looks again. Each board message kept for
// the resume is handed to the CFO first, so one that cannot be leaves the
// resume failed for the next reading to try again.
func finishTakenResume(stateDir, id string) (err error) {
	if _, isTaken, err := takenResume(stateDir, id); err != nil || !isTaken {
		return err
	}
	name := state.LifecycleLockName(id)
	if _, err := lock.AcquireExclusiveNamed(stateDir, name); err != nil {
		return nil
	}
	defer func() { err = errors.Join(err, lock.ReleaseExclusiveNamed(stateDir, name)) }()
	record, isTaken, err := takenResume(stateDir, id)
	if err != nil || !isTaken {
		return err
	}
	var published error
	if _, messages := keptMessages(record.ResumeNote); len(messages) > 0 {
		why := id + "'s failed resume was finished as running because its goblin reported from the session it started; a message kept before that resume began at " + record.Started.UTC().Format(time.RFC3339) + " went with it, and one kept after it failed did not"
		for _, message := range messages {
			if err := handToCFO(stateDir, id, why, message); err != nil {
				return err
			}
		}
		_, published = wake.PublishEpisode(stateDir)
	}
	record.Phase, record.Updated = "running", time.Now().UTC()
	if err := state.WriteLifecycle(stateDir, record); err != nil {
		return errors.Join(published, err)
	}
	return errors.Join(published, state.AppendStatus(stateDir, id, "lifecycle-running: "+state.NormalizeStatusDetail(record.Reason+"; its goblin reported from the session this resume started, so it resumed")))
}

// HandOverRelaunchedNote gives the CFO, with their words, the board messages
// a failed resume's record keeps once a Resume finds the terminal that
// resume's relaunch started already up: that Resume relaunches nothing and
// finishes the task as running, so nothing types them now. A resume cut short
// kept none after its relaunch began, so it hands nothing on. It takes no
// lock and writes nothing: its caller holds the task's lifecycle lock and
// writes its own record over this one once they are handed on. It says
// whether each is on the CFO's notify wake, so a wake episode that fails to
// publish after them is said without handing them on twice.
func HandOverRelaunchedNote(stateDir string, record state.Lifecycle) (isHanded bool, err error) {
	_, messages := keptMessages(record.ResumeNote)
	if !waitsForResume(record) || len(messages) == 0 {
		return true, nil
	}
	why := record.ID + "'s failed resume was finished as running because a Resume found the terminal it started already up; a message kept before that resume began at " + record.Started.UTC().Format(time.RFC3339) + " went with it, and one kept after it failed did not"
	for _, message := range messages {
		if err := handToCFO(stateDir, record.ID, why, message); err != nil {
			return false, err
		}
	}
	_, err = wake.PublishEpisode(stateDir)
	return true, err
}

// takenResume reads task id's lifecycle record and says whether it is a
// failed resume from whose session the goblin has reported working, waiting,
// a question or a delivery since the failure was recorded. A failed resume
// names the session its relaunch started, which ran until a later start of
// the task replaced it, if one did, and its window runs from the recorded
// failure: what the relaunch writes, as Codex's working line on hooks it did
// not trust, comes before it. A failure is not such a report: the resume
// writes its own failure to the same log, in the failure's second. A resume
// whose failure, or a later session whose start, cannot be read has no report
// placed in its session.
func takenResume(stateDir, id string) (state.Lifecycle, bool, error) {
	record, err := state.ReadLifecycle(stateDir, id)
	if errors.Is(err, os.ErrNotExist) {
		return record, false, nil
	}
	if err != nil || record.Action != "resume" || record.Phase != "failed" || record.Updated.IsZero() {
		return record, false, err
	}
	meta, err := state.ReadTaskMeta(stateDir, id)
	if err != nil {
		return record, false, err
	}
	var replaced time.Time
	if meta.SpawnGen != record.Generation {
		if replaced = spawnTime(meta.SpawnGen).Truncate(time.Second); replaced.IsZero() {
			return record, false, nil
		}
	}
	lines, err := state.TailStatus(stateDir, id, 200)
	if err != nil {
		return record, false, err
	}
	failed := record.Updated.Truncate(time.Second)
	for i := len(lines) - 1; i >= 0; i-- {
		stamp, event := state.SplitStatus(lines[i])
		if stamp.Before(failed) {
			break
		}
		if !replaced.IsZero() && !stamp.Before(replaced) {
			continue
		}
		if kind := reportKind(strings.TrimSpace(event)); kind != "" && kind != "failed" {
			return record, true, nil
		}
	}
	return record, false, nil
}
