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
	// cfoWaitWakeAfter is how long a goblin waits on the CFO before its wait
	// wakes the CFO, and cfoWaitWakeGap the least time between two wakes for
	// one wait. On 2026-11-02 a goblin waited two hours on a pull request held
	// for the CFO's review, the fifth such wait since 2026-10-13 the CFO found
	// only by sweeping by hand (#1912).
	cfoWaitWakeAfter = 10 * time.Minute
	cfoWaitWakeGap   = 30 * time.Minute
	// cfoWaitKeyPrefix starts the key each wait's last wake is kept under in
	// fleetWakes.Woke.
	cfoWaitKeyPrefix = "cfo_wait:"
	// cfoWaitTextWidth is how much of a goblin's report or a backlog row's
	// title a wait quotes.
	cfoWaitTextWidth = 200
)

// CFOWait is a live goblin that waits on the CFO, since when, and what for,
// with what the CFO does next.
type CFOWait struct {
	Task   string
	Goblin string
	Since  time.Time
	What   string
	Next   string
}

// Who names the goblin by its name and its task, or by its task alone when
// it has no name.
func (w CFOWait) Who() string {
	if w.Goblin == "" {
		return w.Task
	}
	return w.Goblin + " (" + w.Task + ")"
}

// Minutes is how many whole minutes the goblin has waited at now.
func (w CFOWait) Minutes(now time.Time) int {
	return max(0, int(now.Sub(w.Since)/time.Minute))
}

func (w CFOWait) wakeKey() string {
	return cfoWaitKeyPrefix + w.Task + "@" + w.Since.UTC().Format(time.RFC3339Nano)
}

// CFOWaits reads from h every live goblin that waits on the CFO, the one
// that has waited longest first, as the supervisor's sweep reads them, for
// the session digest and the compact checkpoint. A goblin that could not be
// read is left out and its error returned beside the rest.
func CFOWaits(h home.Home) ([]CFOWait, error) {
	watched, err := readFleetWakes(h.State)
	waits, unread := cfoWaits(h, watched.KeptOff)
	for _, id := range slices.Sorted(maps.Keys(unread)) {
		err = errors.Join(err, unread[id])
	}
	return waits, err
}

// cfoWaits reads every live goblin that waits on the CFO, the one that has
// waited longest first, given why no merge train takes each goblin pull
// request the CI poll found kept off one. unread holds, by goblin, why its
// wait could not be read.
//
// A goblin waits on the CFO while its latest report is a wait on the CFO
// itself, a done on a pull request no train takes because it is a draft or
// held (train.KeptOff, as the last CI poll read it), or a wait on a backlog
// row of kind cfo, which only the CFO does, and nothing has reached it since:
// a report it posts after, a prompt its harness took after, such as the
// CFO's answer, or a pause, a stop or its retirement each end the wait. A
// goblin paused until a backlog row of kind cfo is done waits on the CFO
// too. Any other wait, on CI, a deploy, the Overlord, memory or another
// goblin's task, ends by itself or is someone else's, and is none.
func cfoWaits(h home.Home, keptOff map[string]string) (waits []CFOWait, unread map[string]error) {
	backlog, backlogErr := fleet.ReadBacklog(h)
	cfoRow := func(id string) (fleet.BacklogRow, bool, error) {
		if backlogErr != nil {
			return fleet.BacklogRow{}, false, backlogErr
		}
		for _, row := range slices.Concat(backlog.Queued, backlog.Parked) {
			if row.Structured && row.ID == id {
				return row, strings.EqualFold(row.Kind, "cfo"), nil
			}
		}
		return fleet.BacklogRow{}, false, nil
	}
	unread = map[string]error{}
	for _, meta := range liveTasks(h.State) {
		wait, isWaiting, err := cfoWaitOf(h.State, meta, cfoRow, keptOff)
		if err != nil {
			unread[meta.ID] = fmt.Errorf("whether %s waits on the CFO: %w", meta.ID, err)
			continue
		}
		if isWaiting {
			waits = append(waits, wait)
		}
	}
	slices.SortStableFunc(waits, func(a, b CFOWait) int { return a.Since.Compare(b.Since) })
	return waits, unread
}

// cfoWaitOf is what meta's goblin waits on the CFO for, if it waits on it.
// cfoRow reads the backlog row id names, and whether the CFO owns it.
func cfoWaitOf(stateDir string, meta state.TaskMeta, cfoRow func(id string) (fleet.BacklogRow, bool, error), keptOff map[string]string) (CFOWait, bool, error) {
	wait := CFOWait{Task: meta.ID, Goblin: meta.GoblinName}
	record, err := state.ReadLifecycle(stateDir, meta.ID)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return wait, false, err
	}
	if err == nil && record.Generation == meta.SpawnGen && record.Phase != "running" {
		// A goblin paused, being paused or resumed, stopped or whose last
		// change failed is not at its prompt, so what it reported last waits
		// on nothing: only the pause in force says what holds it.
		if record.Phase != "paused" || record.Pause == nil || record.Pause.Reason != "dependency" {
			return wait, false, nil
		}
		kind, target, _ := strings.Cut(record.Pause.Until, ":")
		if kind != "task" {
			return wait, false, nil
		}
		row, isCFOs, err := cfoRow(target)
		if err != nil || !isCFOs {
			return wait, false, err
		}
		wait.Since = record.Pause.At
		wait.What = "paused until the CFO's own backlog row " + rowName(row) + " is done"
		wait.Next = "finish backlog row " + row.ID + ", then resume " + meta.ID + " with cfo resume " + meta.ID
		return wait, true, nil
	}
	lines, err := state.TailStatus(stateDir, meta.ID, 200)
	if err != nil {
		return wait, false, err
	}
	since, report := latestReport(lines, spawnTime(meta.SpawnGen))
	quoted := cutText(report, cfoWaitTextWidth)
	rest, isWait := strings.CutPrefix(report, "waiting on ")
	target, _, isSeparated := strings.Cut(rest, ": ")
	isWait = isWait && isSeparated
	pullRequest := ""
	if delivered, isDone := strings.CutPrefix(report, "done: PR "); isDone {
		pullRequest, _, _ = strings.Cut(delivered, " ")
	}
	switch {
	case isWait && strings.EqualFold(target, "cfo"):
		wait.What = quoted
		wait.Next = fmt.Sprintf("answer it with cfo send %s \"<your answer>\"", meta.ID)
	case pullRequest != "" && keptOff[pullRequest] != "":
		wait.What = quoted + ", which no merge train takes: " + keptOff[pullRequest]
		wait.Next = fmt.Sprintf("review %s, then clear what keeps it off the train, or tell %s what to change with cfo send %s \"<what>\"", pullRequest, meta.ID, meta.ID)
	case isWait && !slices.Contains([]string{"ci", "deploy", "overlord", "memory"}, target) && state.ValidTaskID(target) == nil:
		row, isCFOs, err := cfoRow(target)
		if err != nil || !isCFOs {
			return wait, false, err
		}
		wait.What = quoted + "; " + row.ID + " is the CFO's own backlog row " + rowName(row)
		wait.Next = fmt.Sprintf("finish backlog row %s and tell %s with cfo send %s \"<what>\"", row.ID, meta.ID, meta.ID)
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
	return row.ID + " (" + cutText(row.Title, cfoWaitTextWidth) + ")"
}

// cutText is text cut to width bytes, never inside a character.
func cutText(text string, width int) string {
	if len(text) <= width {
		return text
	}
	return strings.ToValidUTF8(text[:width], "") + "..."
}

// checkCFOWaits wakes the CFO for each goblin that has waited on it for
// cfoWaitWakeAfter, and again on each later sweep while it still waits, at
// most once every cfoWaitWakeGap. A wait that ended is forgotten, so the
// goblin's next wait wakes the CFO cfoWaitWakeAfter into it; one whose goblin
// could not be read is kept, so a read that fails once wakes nobody again. A
// goblin read failing failingPasses sweeps in a row is an error.
func (s *Service) checkCFOWaits(w *fleetWakes, now time.Time) error {
	waits, unread := cfoWaits(s.Store.Home, w.KeptOff)
	var problems error
	failed := map[string]bool{}
	for _, id := range slices.Sorted(maps.Keys(unread)) {
		failed[id] = true
		problems = errors.Join(problems, w.failing("cfo wait:"+id, unread[id]))
	}
	w.stillFailing("cfo wait:", failed)
	current := map[string]bool{}
	for _, wait := range waits {
		key := wait.wakeKey()
		current[key] = true
		if now.Sub(wait.Since) < cfoWaitWakeAfter || !w.due(key, cfoWaitWakeGap, now) {
			continue
		}
		detail := fmt.Sprintf("cfo_wait: %s has waited %d minutes on the CFO: %s; next: %s", wait.Who(), wait.Minutes(now), wait.What, wait.Next)
		if err := raiseFleetWake(s.Store.Home.State, "check", wait.Task, detail); err != nil {
			problems = errors.Join(problems, err)
			continue
		}
		w.woke(key, now)
	}
	maps.DeleteFunc(w.Woke, func(key string, _ time.Time) bool {
		rest, isWait := strings.CutPrefix(key, cfoWaitKeyPrefix)
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
