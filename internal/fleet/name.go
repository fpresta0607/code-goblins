package fleet

import (
	"io"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/crewstate"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// NameMatch is one task the board calls by a goblin's name: its id, the
// name and title, its state, project and row title, and what it waits on.
type NameMatch struct {
	ID          string `json:"id"`
	GoblinName  string `json:"goblin_name"`
	GoblinTitle string `json:"goblin_title,omitempty"`
	State       string `json:"state"`
	Project     string `json:"project"`
	Title       string `json:"title"`
	WaitsOn     string `json:"waits_on"`
}

// FindByName is every task in snapshot whose goblin is called name, ignoring
// case, newest first: a queued task, then one under way or paused, then
// finished ones, the last to finish first. A name is never held by two
// queued or live goblins at once, so only finished ones can share it with
// another.
func FindByName(snapshot Snapshot, name string) []NameMatch {
	name = strings.TrimSpace(name)
	isCalled := func(goblin string) bool { return goblin != "" && strings.EqualFold(goblin, name) }
	var matches []NameMatch
	for _, row := range snapshot.Backlog.Queued {
		if row.Structured && isCalled(row.GoblinName) {
			matches = append(matches, NameMatch{ID: row.ID, GoblinName: row.GoblinName, GoblinTitle: row.GoblinTitle, State: "queued", Project: row.Repo, Title: row.Title, WaitsOn: queuedWait(row)})
		}
	}
	for _, task := range snapshot.Tasks {
		if isCalled(task.GoblinName) {
			matches = append(matches, NameMatch{ID: task.ID, GoblinName: task.GoblinName, GoblinTitle: task.GoblinTitle, State: string(task.Current.State), Project: projectName(task.Project), Title: task.Title, WaitsOn: liveWait(task)})
		}
	}
	finished := slices.Clone(snapshot.Completed)
	slices.SortStableFunc(finished, func(a, b state.Outcome) int { return b.At.Compare(a.At) })
	for _, outcome := range finished {
		if isCalled(outcome.GoblinName) {
			phase := "stopped"
			if outcome.Phase == "done" {
				phase = "finished"
			}
			matches = append(matches, NameMatch{ID: outcome.ID, GoblinName: outcome.GoblinName, GoblinTitle: outcome.GoblinTitle, State: phase, Project: projectName(outcome.Project), Title: outcome.Title})
		}
	}
	return matches
}

// queuedWait is what a queued row waits on: each blocked-by and its hold,
// then the reason its row gives.
func queuedWait(row BacklogRow) string {
	waits := make([]string, 0, len(row.Blockers))
	for _, blocker := range row.Blockers {
		if blocker.Kind == "hold" {
			waits = append(waits, "hold: "+blocker.Target)
		} else {
			waits = append(waits, blocker.Target)
		}
	}
	wait := strings.Join(waits, ", ")
	if row.BlockedReason != "" {
		wait += " - " + row.BlockedReason
	}
	return wait
}

// liveWait is what a live goblin waits on: what its pause waits for, or the
// words of its latest report when that says it waits or is blocked.
func liveWait(task TaskRow) string {
	if task.Pause != nil {
		return task.Pause.Description()
	}
	if task.Current.State == crewstate.Waiting || task.Current.State == crewstate.Blocked {
		return task.Current.Detail
	}
	return ""
}

// projectName is the project a task's record names by its checkout, as the
// board shows it.
func projectName(project string) string {
	if project == "" {
		return ""
	}
	return filepath.Base(project)
}

// RenderNameMatches writes the tasks FindByName found as a Markdown table.
func RenderNameMatches(w io.Writer, matches []NameMatch) error {
	if err := writeLine(w, "| Task | Goblin | State | Project | Title | Waits On |\n| --- | --- | --- | --- | --- | --- |"); err != nil {
		return err
	}
	for _, match := range matches {
		if err := writeLine(w, "| "+strings.Join(markdownFields([]string{match.ID, goblinText(match.GoblinName, match.GoblinTitle), match.State, match.Project, match.Title, match.WaitsOn}), " | ")+" |"); err != nil {
			return err
		}
	}
	return nil
}
