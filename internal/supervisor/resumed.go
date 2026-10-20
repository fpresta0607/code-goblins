package supervisor

import (
	"errors"
	"os"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// A resume is recorded failed when its relaunched harness was not seen
// working on its instruction within the resume's window, though the goblin
// may take it after: on 2026-10-20 Teddy's resumed Enter did not take on a
// loaded machine, the CFO pressed it by hand, and Teddy worked on in the
// resumed session while the board drew "Resume failed" for hours and kept the
// Overlord's messages for a resume that never came. A goblin that reports
// from the session its resume started has resumed, so its record is finished
// as running.

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
// record to it, and the next reading looks again.
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
	record.Phase, record.Updated = "running", time.Now().UTC()
	if err := state.WriteLifecycle(stateDir, record); err != nil {
		return err
	}
	return state.AppendStatus(stateDir, id, "lifecycle-running: "+state.NormalizeStatusDetail(record.Reason+"; its goblin reported from the session this resume started, so it resumed"))
}

// takenResume reads task id's lifecycle record and says whether it is a
// failed resume from whose session the goblin has reported working, waiting,
// a question or a delivery. A failed resume names the session its relaunch
// started, which ran from the resume's start until a later start of the task
// replaced it, if one did. A failure is not such a report: the resume writes
// its own failure to the same log. A resume whose start, or a later session
// whose start, cannot be read has no report placed in its session.
func takenResume(stateDir, id string) (state.Lifecycle, bool, error) {
	record, err := state.ReadLifecycle(stateDir, id)
	if errors.Is(err, os.ErrNotExist) {
		return record, false, nil
	}
	if err != nil || record.Action != "resume" || record.Phase != "failed" || record.Started.IsZero() {
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
	began := record.Started.Truncate(time.Second)
	for i := len(lines) - 1; i >= 0; i-- {
		stamp, event := state.SplitStatus(lines[i])
		if stamp.Before(began) {
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
