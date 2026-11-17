package supervisor

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/train"
)

const (
	// ceoWaitWakeAfter is how long a goblin waits on the CEO before its wait
	// wakes the CEO, and ceoWaitWakeGap the least time between two wakes for
	// one wait. On 2026-11-02 a goblin waited two hours on a pull request held
	// for the CEO's review, the fifth such wait since 2026-10-13 the CEO found
	// only by sweeping by hand (#1912).
	ceoWaitWakeAfter = 10 * time.Minute
	ceoWaitWakeGap   = 30 * time.Minute
	// ceoWaitKeyPrefix starts the key each wait's last wake is kept under in
	// fleetWakes.Woke.
	ceoWaitKeyPrefix = "ceo_wait:"
	// ceoWaitTextWidth is how much of a goblin's report or a backlog row's
	// title a wait quotes.
	ceoWaitTextWidth = 200
)

// CEOWait is a live goblin that waits on the CEO, since when, and what for,
// with what the CEO does next.
type CEOWait struct {
	Task   string
	Goblin string
	Since  time.Time
	What   string
	Next   string
}

// Who names the goblin by its name and its task, or by its task alone when
// it has no name.
func (w CEOWait) Who() string {
	if w.Goblin == "" {
		return w.Task
	}
	return w.Goblin + " (" + w.Task + ")"
}

// Minutes is how many whole minutes the goblin has waited at now.
func (w CEOWait) Minutes(now time.Time) int {
	return max(0, int(now.Sub(w.Since)/time.Minute))
}

func (w CEOWait) wakeKey() string {
	return ceoWaitKeyPrefix + w.Task + "@" + w.Since.UTC().Format(time.RFC3339Nano)
}

// CEOWaits reads from h every live goblin that waits on the CEO, the one
// that has waited longest first, as the supervisor's sweep reads them, for
// the session digest and the compact checkpoint. A goblin that could not be
// read is left out and its error returned beside the rest.
func CEOWaits(h home.Home) ([]CEOWait, error) {
	watched, err := readFleetWakes(h.State)
	waits, unread := ceoWaits(h, watched.KeptOff)
	for _, id := range slices.Sorted(maps.Keys(unread)) {
		err = errors.Join(err, unread[id])
	}
	return waits, err
}

// ceoWaits reads every live goblin that waits on the CEO, the one that has
// waited longest first, given why no merge train takes each goblin pull
// request the CI poll found kept off one. unread holds, by goblin, why its
// wait could not be read.
//
// A goblin waits on the CEO while its latest report is a wait on the CEO
// itself, a done on a pull request no train takes because it is a draft or
// held (train.KeptOff, as the last CI poll read it), or a wait on a backlog
// row of kind ceo, which only the CEO does, and nothing has reached it since:
// a report it posts after, a prompt its harness took after, such as the
// CEO's answer, or a pause that took hold, a stop or its retirement each end
// the wait. A goblin paused until a backlog row of kind ceo is done waits on
// the CEO too. Such a row still holds its goblin once the CEO moves it to
// Done, since nothing tells the goblin or resumes it but the CEO. Any other
// wait, on CI, a deploy, the Overlord, memory or another goblin's task, ends
// by itself or is someone else's, and is none.
func ceoWaits(h home.Home, keptOff map[string]string) (waits []CEOWait, unread map[string]error) {
	backlog, backlogErr := fleet.ReadBacklog(h)
	ceoRow := func(id string) (row fleet.BacklogRow, isDone, isCEOs bool, err error) {
		if backlogErr != nil {
			return fleet.BacklogRow{}, false, false, backlogErr
		}
		isRow := func(row fleet.BacklogRow) bool { return row.Structured && row.ID == id }
		open := slices.Concat(backlog.Queued, backlog.Parked)
		if index := slices.IndexFunc(open, isRow); index >= 0 {
			return open[index], false, strings.EqualFold(open[index].Kind, "ceo"), nil
		}
		if index := slices.IndexFunc(backlog.Done, isRow); index >= 0 {
			return backlog.Done[index], true, strings.EqualFold(backlog.Done[index].Kind, "ceo"), nil
		}
		return fleet.BacklogRow{}, false, false, nil
	}
	unread = map[string]error{}
	for _, meta := range liveTasks(h.State) {
		wait, isWaiting, err := ceoWaitOf(h.State, meta, ceoRow, keptOff)
		if err != nil {
			unread[meta.ID] = fmt.Errorf("whether %s waits on the CEO: %w", meta.ID, err)
			continue
		}
		if isWaiting {
			waits = append(waits, wait)
		}
	}
	slices.SortStableFunc(waits, func(a, b CEOWait) int { return a.Since.Compare(b.Since) })
	return waits, unread
}

// ceoWaitOf is what meta's goblin waits on the CEO for, if it waits on it.
// ceoRow reads the backlog row id names, whether it is done, and whether the
// CEO owns it.
func ceoWaitOf(stateDir string, meta state.TaskMeta, ceoRow func(id string) (row fleet.BacklogRow, isDone, isCEOs bool, err error), keptOff map[string]string) (CEOWait, bool, error) {
	wait := CEOWait{Task: meta.ID, Goblin: meta.GoblinName}
	record, err := state.ReadLifecycle(stateDir, meta.ID)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return wait, false, err
	}
	isPaused := record.Phase == "paused" || pauseTookHold(stateDir, meta.Backend, record)
	if err == nil && record.Generation == meta.SpawnGen && (isPaused || record.SuppressesMonitoring(stateDir)) {
		// A goblin paused, stopped, or being paused, resumed or stopped is
		// not at its prompt, so what it reported last waits on nothing: only
		// the pause in force says what holds it. One whose change failed
		// carries on at its work, as its progress check reads it, unless it
		// was a pause that took hold since.
		if !isPaused || record.Pause == nil || record.Pause.Reason != "dependency" {
			return wait, false, nil
		}
		kind, target, _ := strings.Cut(record.Pause.Until, ":")
		if kind != "task" {
			return wait, false, nil
		}
		row, isDone, isCEOs, err := ceoRow(target)
		if err != nil || !isCEOs {
			return wait, false, err
		}
		wait.Since = record.Pause.At
		if isDone {
			wait.What = "paused until the CEO's own backlog row " + rowName(row) + ", which is done"
			wait.Next = "resume " + meta.ID + " with goblins resume " + meta.ID
		} else {
			wait.What = "paused until the CEO's own backlog row " + rowName(row) + " is done"
			wait.Next = "finish backlog row " + row.ID + ", then resume " + meta.ID + " with goblins resume " + meta.ID
		}
		return wait, true, nil
	}
	lines, err := state.TailStatus(stateDir, meta.ID, 200)
	if err != nil {
		return wait, false, err
	}
	since, report := latestReport(lines, spawnTime(meta.SpawnGen))
	quoted := bounded(report, ceoWaitTextWidth)
	rest, isWait := strings.CutPrefix(report, "waiting on ")
	target, _, isSeparated := strings.Cut(rest, ": ")
	isWait = isWait && isSeparated
	pullRequest := ""
	if delivered, isDone := strings.CutPrefix(report, "done: PR "); isDone {
		pullRequest, _, _ = strings.Cut(delivered, " ")
	}
	switch {
	case isWait && strings.EqualFold(target, "ceo"):
		wait.What = quoted
		wait.Next = fmt.Sprintf("answer it with goblins send %s \"<your answer>\"", meta.ID)
	case pullRequest != "" && keptOff[pullRequest] != "":
		wait.What = quoted + ", which no merge train takes: " + keptOff[pullRequest]
		wait.Next = fmt.Sprintf("review %s, then clear what keeps it off the train, or tell %s what to change with goblins send %s \"<what>\"", pullRequest, meta.ID, meta.ID)
	case isWait && !slices.Contains(selfEndingWaits, target) && state.ValidTaskID(target) == nil:
		row, isDone, isCEOs, err := ceoRow(target)
		if err != nil || !isCEOs {
			return wait, false, err
		}
		wait.What = quoted + "; " + row.ID + " is the CEO's own backlog row " + rowName(row)
		wait.Next = fmt.Sprintf("finish backlog row %s and tell %s with goblins send %s \"<what>\"", row.ID, meta.ID, meta.ID)
		if isDone {
			wait.What += ", done in the backlog"
			wait.Next = fmt.Sprintf("tell %s that %s is done with goblins send %s \"<what>\"", meta.ID, row.ID, meta.ID)
		}
	default:
		return wait, false, nil
	}
	wait.Since = since
	answered, err := NativePromptSince(stateDir, meta.ID, meta.SpawnGen, since)
	if err != nil || answered {
		return wait, false, err
	}
	return wait, true, nil
}

// rowName is a backlog row's id and its title, cut short.
func rowName(row fleet.BacklogRow) string {
	if row.Title == "" {
		return row.ID
	}
	return row.ID + " (" + bounded(row.Title, ceoWaitTextWidth) + ")"
}

// checkCEOWaits wakes the CEO for each goblin that has waited on it for
// ceoWaitWakeAfter, and again on each later sweep while it still waits, at
// most once every ceoWaitWakeGap. A wait that ended is forgotten, so the
// goblin's next wait wakes the CEO ceoWaitWakeAfter into it; one whose goblin
// could not be read is kept, so a read that fails once wakes nobody again. A
// goblin read failing failingPasses sweeps in a row is an error.
func (s *Service) checkCEOWaits(w *fleetWakes, now time.Time) error {
	waits, unread := ceoWaits(s.Store.Home, w.KeptOff)
	var problems error
	failed := map[string]bool{}
	for _, id := range slices.Sorted(maps.Keys(unread)) {
		failed[id] = true
		problems = errors.Join(problems, w.failing("ceo wait:"+id, unread[id]))
	}
	w.stillFailing("ceo wait:", failed)
	current := map[string]bool{}
	for _, wait := range waits {
		key := wait.wakeKey()
		current[key] = true
		if now.Sub(wait.Since) < ceoWaitWakeAfter || !w.due(key, ceoWaitWakeGap, now) {
			continue
		}
		detail := fmt.Sprintf("ceo_wait: %s has waited %d minutes on the CEO: %s; next: %s", wait.Who(), wait.Minutes(now), wait.What, wait.Next)
		if err := raiseFleetWake(s.Store.Home.State, "check", wait.Task, detail); err != nil {
			problems = errors.Join(problems, err)
			continue
		}
		w.woke(key, now)
	}
	maps.DeleteFunc(w.Woke, func(key string, _ time.Time) bool {
		rest, isWait := strings.CutPrefix(key, ceoWaitKeyPrefix)
		task, _, _ := strings.Cut(rest, "@")
		return isWait && !current[key] && !failed[task]
	})
	return problems
}

// recordKeptOff keeps, for each pull request goblins reported, why no merge
// train takes it while open, the repository's open pull requests, lists it
// kept off, and forgets one open lists as one a train may take or, read
// whole, no longer lists: it merged or closed.
func (w *fleetWakes) recordKeptOff(goblins []ciGoblin, open []train.PullRequest) {
	for _, goblin := range goblins {
		for _, url := range goblin.pullRequests {
			index := slices.IndexFunc(open, func(pr train.PullRequest) bool { return pr.URL == url })
			switch {
			case index >= 0 && train.KeptOff(open[index]) != "":
				if w.KeptOff == nil {
					w.KeptOff = map[string]string{}
				}
				w.KeptOff[url] = train.KeptOff(open[index])
			case index >= 0 || len(open) < 100:
				delete(w.KeptOff, url)
			}
		}
	}
}
