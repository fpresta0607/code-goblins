package supervisor

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// The board reads the fleet's own records for work no native hook reported:
// task metadata, status logs, the wake queue, the gate database and what
// cfo cleanup leaves behind. Without them a goblin that never installed the
// native hooks read as awaiting evidence however busy it was.

const (
	historyWindow = 7 * 24 * time.Hour
	historyLimit  = 20
)

var (
	archivedStatusFile = regexp.MustCompile(`^(.+)\.status\.(\d{8}T\d{6}Z)$`)
	archivedTaskDir    = regexp.MustCompile(`^(.+)\.(\d{8}T\d{6}Z)$`)
)

// fleetEvaluation is the status of a task no native hook has reported: a
// question it is still waiting on first, then what the gate proved, then what
// Herdr sees in its pane.
func fleetEvaluation(evaluation Evaluation, meta state.TaskMeta, runtime RuntimeEvidence, records []wake.Record) Evaluation {
	if evaluation.Generation != meta.SpawnGen {
		evaluation = Evaluation{Generation: meta.SpawnGen}
	}
	if verb, detail, ok := waitingQuestion(records, meta.ID); ok {
		evaluation.Phase, evaluation.Reason = verb, "Waiting on the CFO: "+detail
		return evaluation
	}
	switch evaluation.Phase {
	case "blocked", "ready", "merged", "done":
		return evaluation
	}
	switch {
	case runtime.working():
		evaluation.Phase, evaluation.Reason = "working", runtime.Reason
	case runtime.State == string(monitor.HealthIdle) || runtime.State == string(monitor.HealthParked):
		evaluation.Phase, evaluation.Reason = "idle", runtime.Reason
	case evaluation.Phase == "":
		evaluation.Phase, evaluation.Reason = "unknown", runtime.Reason
	}
	return evaluation
}

// waitingQuestion is the blocked or failed notify a task is still waiting on.
func waitingQuestion(records []wake.Record, id string) (string, string, bool) {
	for i := len(records) - 1; i >= 0; i-- {
		if verb, ok := wake.BlockingNotify(records[i]); ok && records[i].Key == id {
			return verb, strings.TrimSpace(strings.TrimPrefix(records[i].Detail, verb+":")), true
		}
	}
	return "", "", false
}

// statusActivity is a task's own latest status line, the one-line answer to
// what it is doing now, and the pull request it last reported done.
func statusActivity(lines []string) (string, string) {
	activity, pr := "", ""
	for i := len(lines) - 1; i >= 0; i-- {
		_, event := state.SplitStatus(lines[i])
		event = strings.TrimSpace(event)
		if activity == "" {
			activity = event
		}
		// Only an https link is a pull request; the line is goblin-written.
		if rest, ok := strings.CutPrefix(event, "done: PR "); ok && pr == "" {
			if fields := strings.Fields(rest); len(fields) > 0 && strings.HasPrefix(fields[0], "https://") {
				pr = fields[0]
			}
		}
		if activity != "" && pr != "" {
			break
		}
	}
	return bounded(activity, 300), pr
}

// finishedTasks are tasks cfo cleanup finished within the history window,
// newest first. Cleanup leaves the status log in place with no task record
// beside it; cfo reap later moves it into the archive as its own file, and
// older archives keep it inside the task's archived directory.
func finishedTasks(stateDir string, now time.Time) []Task {
	type finished struct {
		at   time.Time
		path string
	}
	found := map[string]finished{}
	keep := func(id, path string, at time.Time) {
		if state.ValidTaskID(id) != nil || now.Sub(at) > historyWindow {
			return
		}
		if prior, ok := found[id]; !ok || at.After(prior.at) {
			found[id] = finished{at, path}
		}
	}
	if entries, err := os.ReadDir(stateDir); err == nil {
		for _, entry := range entries {
			id, ok := strings.CutSuffix(entry.Name(), ".status")
			if !ok || entry.IsDir() {
				continue
			}
			if _, err := os.Stat(filepath.Join(stateDir, id+".meta")); err == nil {
				continue
			}
			if info, err := entry.Info(); err == nil {
				keep(id, filepath.Join(stateDir, entry.Name()), info.ModTime())
			}
		}
	}
	archive := filepath.Join(stateDir, state.ArchiveDirName)
	if entries, err := os.ReadDir(archive); err == nil {
		for _, entry := range entries {
			if m := archivedStatusFile.FindStringSubmatch(entry.Name()); m != nil && !entry.IsDir() {
				if at, err := time.Parse("20060102T150405Z", m[2]); err == nil {
					keep(m[1], filepath.Join(archive, entry.Name()), at)
				}
			} else if m := archivedTaskDir.FindStringSubmatch(entry.Name()); m != nil && entry.IsDir() {
				path := filepath.Join(archive, entry.Name(), m[1]+".status")
				if at, err := time.Parse("20060102T150405Z", m[2]); err == nil && exists(path) {
					keep(m[1], path, at)
				}
			}
		}
	}
	tasks := []Task{}
	for id, f := range found {
		lines, err := fsx.ReadLines(f.path)
		if err != nil {
			continue
		}
		_, pr := statusActivity(lines)
		tasks = append(tasks, Task{ID: "finished:" + id, Title: id, Dependencies: []string{}, Archived: true, Evaluation: Evaluation{Phase: "done", PR: pr, Reason: "Finished and cleaned up", At: f.at}})
	}
	return newestHistory(tasks)
}

// withMergedPRs adds the pull requests the gate saw merged to the finished
// tasks: a finished task that reported the same pull request carries the
// merge, and any other merged pull request is its own completed entry.
func withMergedPRs(history []Task, merged []pipeline.MergedPR) []Task {
	for _, pr := range merged {
		found := false
		for i := range history {
			if history[i].PR == pr.PR {
				history[i].Merged, found = true, true
			}
		}
		if !found {
			history = append(history, Task{ID: "merged:" + pr.PR, Title: pr.Branch, Project: filepath.Base(pr.Project), Dependencies: []string{}, Archived: true, Merged: true, Evaluation: Evaluation{Phase: "done", PR: pr.PR, Reason: "Pull request merged", At: time.Unix(pr.At, 0).UTC()}})
		}
	}
	return newestHistory(history)
}

func newestHistory(tasks []Task) []Task {
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].At.After(tasks[j].At) })
	if len(tasks) > historyLimit {
		tasks = tasks[:historyLimit]
	}
	return tasks
}

// queuedBriefs are briefs nothing has started: no live task record, and no
// status log or archive entry, which every dispatched task leaves.
func queuedBriefs(h home.Home) []Task {
	entries, err := os.ReadDir(h.Data)
	if err != nil {
		return nil
	}
	archived, _ := os.ReadDir(filepath.Join(h.State, state.ArchiveDirName))
	tasks := []Task{}
	for _, entry := range entries {
		id := entry.Name()
		if !entry.IsDir() || state.ValidTaskID(id) != nil || !exists(filepath.Join(h.Data, id, "brief.md")) {
			continue
		}
		if exists(filepath.Join(h.State, id+".meta")) || exists(filepath.Join(h.State, id+".status")) {
			continue
		}
		dispatched := false
		for _, a := range archived {
			if strings.HasPrefix(a.Name(), id+".") {
				dispatched = true
				break
			}
		}
		if !dispatched {
			tasks = append(tasks, Task{ID: id, Title: id, Dependencies: []string{}, Evaluation: Evaluation{Phase: "queued", Reason: "Brief ready at data/" + id + "/brief.md; not dispatched yet"}})
		}
	}
	return tasks
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
