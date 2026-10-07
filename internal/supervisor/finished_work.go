package supervisor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// finishedWork is what says a queued task already finished, read once for a
// pass over the queue: the names of the archived status logs and the fleet's
// history. A task whose outcome says it delivered is not queued at all
// (fleet.ReadQueuedTask); this reads everything else that says so. On
// 2026-10-07 the CFO had left 24 finished rows under Queued, each with its
// brief on disk, which the scheduler would have started again.
type finishedWork struct {
	home     home.Home
	archived []string
	history  []Task
}

// readFinishedWork lists the archived status logs once; history is the
// fleet's, or nil where it is not kept, as in a hook.
func readFinishedWork(h home.Home, history []Task) finishedWork {
	work := finishedWork{home: h, history: history}
	entries, _ := os.ReadDir(filepath.Join(h.State, state.ArchiveDirName))
	for _, entry := range entries {
		if !entry.IsDir() && strings.Contains(entry.Name(), ".status.") {
			work.archived = append(work.archived, entry.Name())
		}
	}
	return work
}

// finishedWork reads what says queued work finished, with the fleet's
// history as the supervisor last built it.
func (s *Service) finishedWork() finishedWork {
	s.mu.Lock()
	history := slices.Clone(s.history)
	s.mu.Unlock()
	return readFinishedWork(s.Store.Home, history)
}

// refusal is why id never starts again by itself, for its Start and its card,
// or empty when nothing says it finished.
func (work finishedWork) refusal(id string) string {
	evidence := work.evidence(id)
	if evidence == "" {
		return ""
	}
	return "Already finished: " + evidence + "; it never starts again by itself. Move its row to ## Done, or queue new work under a new id"
}

// evidence says how id finished: its last report, in its live status log or
// the newest archived one, was done; or its pull request merged, or one of
// the branch its last generation worked on after it started, as the fleet's
// history knows.
func (work finishedWork) evidence(id string) string {
	lines, _ := state.TailStatus(work.home.State, id, 200)
	if len(lines) == 0 {
		prefix := id + ".status."
		for _, name := range slices.Backward(work.archived) {
			if strings.HasPrefix(name, prefix) {
				lines, _ = fsx.ReadLines(filepath.Join(work.home.State, state.ArchiveDirName, name))
				break
			}
		}
	}
	if stamp, report := latestReport(lines, time.Time{}); strings.HasPrefix(report, "done:") {
		return "its last report was done (" + stamp.UTC().Format("2006-01-02 15:04Z") + ": " + bounded(report, 200) + ")"
	}
	for _, task := range work.history {
		if task.ID == "finished:"+id && task.Merged {
			return "its pull request merged (" + task.PR + ")"
		}
	}
	outcome, err := state.ReadOutcome(work.home.State, id)
	if err != nil || outcome.Branch == "" {
		return ""
	}
	for _, task := range work.history {
		if task.Merged && task.Branch == outcome.Branch && task.Project == filepath.Base(outcome.Project) && task.At.After(spawnTime(outcome.Generation)) {
			return "its branch " + outcome.Branch + "'s pull request merged (" + task.PR + ")"
		}
	}
	return ""
}

// closeDeliveredRows moves each task under ## Queued whose outcome says it
// delivered to ## Done, as cleanup does when it retires one: for a task an
// older build retired, or one whose cleanup met the backlog's lock. A task
// live again keeps its row, and a lock held by a start in flight is left for
// the next pass.
func (s *Service) closeDeliveredRows() error {
	backlog, err := fleet.ReadBacklog(s.Store.Home)
	if err != nil {
		return err
	}
	var problems error
	for _, row := range backlog.Queued {
		if !row.Structured || state.ValidTaskID(row.ID) != nil {
			continue
		}
		if outcome, err := state.ReadOutcome(s.Store.Home.State, row.ID); err != nil || outcome.Phase != "done" {
			continue
		}
		if _, err := os.Stat(state.TaskMetaPath(s.Store.Home.State, row.ID)); err == nil {
			continue
		}
		err := fleet.CompleteQueuedTask(s.Store.Home, row.ID)
		if err != nil && !errors.Is(err, fleet.ErrNotQueued) && !errors.Is(err, lock.ErrHeld) {
			problems = errors.Join(problems, fmt.Errorf("move %s's backlog row to ## Done: %w", row.ID, err))
		}
	}
	return problems
}
