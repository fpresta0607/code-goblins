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

// finishedWork is what says a queued task already finished, for a pass over
// the queue: the archived status logs, listed once on the first need, and the
// fleet's history. A task whose outcome says it delivered is not queued at
// all (fleet.ReadQueuedTask); this reads everything else that says so. On
// 2026-10-07 the CFO had left 24 finished rows under Queued, each with its
// brief on disk, which the scheduler would have started again.
type finishedWork struct {
	home     home.Home
	history  []Task
	isListed bool
	archived []string
}

// readFinishedWork reads with history, the fleet's, or nil where it is not
// kept, as in a hook.
func readFinishedWork(h home.Home, history []Task) *finishedWork {
	return &finishedWork{home: h, history: history}
}

// finishedWork reads what says queued work finished, with the fleet's
// history as the supervisor last built it.
func (s *Service) finishedWork() *finishedWork {
	s.mu.Lock()
	history := slices.Clone(s.history)
	s.mu.Unlock()
	return readFinishedWork(s.Store.Home, history)
}

// refusal is why id never starts again by itself, for its Start and its card,
// or empty when nothing says it finished.
func (work *finishedWork) refusal(id string) string {
	evidence := work.reportEvidence(id)
	if evidence == "" {
		outcome, err := state.ReadOutcome(work.home.State, id)
		evidence = work.mergeEvidence(id, outcome, err == nil)
	}
	return finishedRefusal(evidence)
}

// finishedRefusal is the refusal evidence makes, or empty without any.
func finishedRefusal(evidence string) string {
	if evidence == "" {
		return ""
	}
	return "Already finished: " + evidence + "; it never starts again by itself. Move its row to ## Done, or queue new work under a new id"
}

// reportEvidence says id finished when its last report, in its live status
// log or else the newest archived one, was done.
func (work *finishedWork) reportEvidence(id string) string {
	lines, _ := state.TailStatus(work.home.State, id, 200)
	if len(lines) == 0 {
		if !work.isListed {
			work.isListed = true
			entries, _ := os.ReadDir(filepath.Join(work.home.State, state.ArchiveDirName))
			for _, entry := range entries {
				if !entry.IsDir() && strings.Contains(entry.Name(), ".status.") {
					work.archived = append(work.archived, entry.Name())
				}
			}
		}
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
	return ""
}

// mergeEvidence says id finished when the fleet's history knows its pull
// request merged, or one of the branch its last generation worked on, after
// that generation started; outcome is its outcome record, when it has one.
func (work *finishedWork) mergeEvidence(id string, outcome state.Outcome, hasOutcome bool) string {
	for _, task := range work.history {
		if task.ID == "finished:"+id && task.Merged {
			return "its pull request merged (" + task.PR + ")"
		}
	}
	if !hasOutcome || outcome.Branch == "" {
		return ""
	}
	for _, task := range work.history {
		if task.Merged && task.Branch == outcome.Branch && task.Project == filepath.Base(outcome.Project) && task.At.After(spawnTime(outcome.Generation)) {
			return "its branch " + outcome.Branch + "'s pull request merged (" + task.PR + ")"
		}
	}
	return ""
}

// finishedCard is a queued card's refusal, read the way the snapshot reads
// every file: the status-log evidence and the outcome again only once their
// files changed, the history every time, since it changes with no file.
func (s *Service) finishedCard(work *finishedWork, id string) string {
	directory := s.Store.Home.State
	report, _ := kept(&s.reads, "finished-report", []string{state.StatusPath(directory, id), filepath.Join(directory, state.ArchiveDirName)}, func() (string, error) {
		return work.reportEvidence(id), nil
	})
	if report != "" {
		return finishedRefusal(report)
	}
	outcome, err := kept(&s.reads, "outcome", []string{filepath.Join(directory, "outcomes", id+".json")}, func() (state.Outcome, error) {
		return state.ReadOutcome(directory, id)
	})
	return finishedRefusal(work.mergeEvidence(id, outcome, err == nil))
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
