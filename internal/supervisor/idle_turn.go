package supervisor

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// idleTurnFile holds the next work the last idle turn wake named, and when,
// so the same next work wakes an idle CFO once per idleWakeAfter.
const idleTurnFile = ".idle-turn.json"

type idleTurn struct {
	Next string    `json:"next"`
	At   time.Time `json:"at"`
}

// WorkWaits reports whether a queued task a Start could start waits, so the
// CFO's Stop hook keeps watching for the wakes it raises though no goblin is
// in flight.
func WorkWaits(h home.Home) bool {
	return len(startableQueued(h, readFinishedWork(h, nil))) > 0
}

// IdleTurnWake wakes a CFO whose turn ends with no goblin at work while work
// that could run waits and memory and commit are free, and returns the wake's
// text, which names the next work: a paused goblin whose pause cleared, oldest
// pause first, then the top of the queue. It returns "" when the turn may
// end: a goblin works, memory is short, nothing could run, or this same next
// work woke an idle turn less than idleWakeAfter ago. The CFO's turn end is
// the one moment the supervisor's scheduler cannot see, so the hook that sees
// it calls this, whatever harness the CFO runs.
func IdleTurnWake(h home.Home, memory Memory, now time.Time) (string, error) {
	if memory.shortfall() != "" {
		return "", nil
	}
	var paused []state.Lifecycle
	for _, meta := range liveTasks(h.State) {
		record, err := state.ReadLifecycle(h.State, meta.ID)
		if err != nil || record.Generation != meta.SpawnGen || record.Phase != "paused" && record.Phase != "stopped" {
			return "", nil
		}
		if record.Phase != "paused" || record.Pause == nil {
			continue
		}
		if isCleared, err := pauseClearedHere(h.State, *record.Pause, now); isCleared && err == nil || record.Pause.Reason == "memory" {
			paused = append(paused, record)
		}
	}
	slices.SortFunc(paused, func(left, right state.Lifecycle) int {
		if order := left.Pause.At.Compare(right.Pause.At); order != 0 {
			return order
		}
		return strings.Compare(left.ID, right.ID)
	})
	finished := readFinishedWork(h, nil)
	queued := startableQueued(h, finished)
	var steps, waiting []string
	for index, record := range paused {
		if index == 0 {
			steps = append(steps, "resume "+record.ID+" with cfo resume "+record.ID)
		} else {
			waiting = append(waiting, record.ID)
		}
	}
	for index, id := range queued {
		if index > 0 {
			waiting = append(waiting, id)
			continue
		}
		plan, err := planStart(h, id, finished)
		if err != nil {
			return "", err
		}
		steps = append(steps, "start "+id+" with cfo "+commandLine(plan.args()))
	}
	if len(steps) == 0 {
		return "", nil
	}
	next := strings.Fields(steps[0])[1]
	path := filepath.Join(h.State, idleTurnFile)
	var last idleTurn
	if data, err := fsx.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &last)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if last.Next == next && !now.Before(last.At) && now.Sub(last.At) < idleWakeAfter {
		return "", nil
	}
	detail := fmt.Sprintf("idle: the CFO's turn ended with no goblin at work while work waits and %.1f GB of memory and %.1f GB of commit are free; next: %s",
		gigabytes(memory.Available), gigabytes(memory.CommitAvailable), strings.Join(steps, ", then "))
	if len(waiting) > 0 {
		detail += "; also waiting: " + strings.Join(waiting[:min(len(waiting), 8)], ", ")
		if len(waiting) > 8 {
			detail += fmt.Sprintf(" and %d more", len(waiting)-8)
		}
	}
	detail += ". The supervisor's scheduler starts them one at a time as memory allows: read cfo fleet-view first, start or resume what has not started, and park a row that must not start."
	if err := raiseFleetWake(h.State, "idle", "turn", detail); err != nil {
		return "", err
	}
	data, err := json.Marshal(idleTurn{Next: next, At: now})
	if err != nil {
		return "", err
	}
	return detail, fsx.AtomicWriteFile(path, data)
}

// startableQueued names the queued tasks a Start could start now, in queue
// order.
func startableQueued(h home.Home, finished finishedWork) []string {
	var queued []string
	for _, id := range queuedCandidates(h) {
		if _, err := planStart(h, id, finished); err == nil {
			queued = append(queued, id)
		}
	}
	return queued
}

// pauseClearedHere says whether a pause has cleared as far as this machine
// sees without asking GitHub, the board or a run of memory readings: an
// allowance or a date once it passed, a task once its outcome says it
// delivered. Every other pause has not, as far as this reading goes.
func pauseClearedHere(stateDir string, condition state.PauseCondition, now time.Time) (bool, error) {
	switch condition.Reason {
	case "allowance":
		at, err := time.Parse(time.RFC3339, condition.Until)
		return err == nil && !now.Before(at), err
	case "dependency":
		kind, target, _ := strings.Cut(condition.Until, ":")
		switch kind {
		case "date":
			at, err := time.Parse(time.RFC3339, target)
			return err == nil && !now.Before(at), err
		case "task":
			outcome, err := state.ReadOutcome(stateDir, target)
			if errors.Is(err, os.ErrNotExist) {
				return false, nil
			}
			return err == nil && outcome.Phase == "done", err
		}
	}
	return false, nil
}

// commandLine is args as one command line, quoting each that holds a space.
func commandLine(args []string) string {
	quoted := make([]string, len(args))
	for index, arg := range args {
		if strings.ContainsAny(arg, " \t") {
			arg = `"` + arg + `"`
		}
		quoted[index] = arg
	}
	return strings.Join(quoted, " ")
}
