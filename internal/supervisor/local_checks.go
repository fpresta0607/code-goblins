package supervisor

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/verify"
)

// LocalChecks is the newest cfo gate test run of a task's change, as its
// report says: one the task's goblin ran, or one of the commit the goblin or
// its pull request is at, as a gate's test step runs it. Level is the level it
// ran and Required the level the change requires; Status is passed or failed;
// Seconds is how long it took and Waited how much of that it waited for its
// turn on the machine; Failed names the packages, or the commands, that did
// not pass.
type LocalChecks struct {
	Commit   string    `json:"commit"`
	Level    string    `json:"level"`
	Required string    `json:"required_level"`
	Status   string    `json:"status"`
	Seconds  float64   `json:"duration_seconds"`
	Waited   float64   `json:"queue_seconds,omitempty"`
	Failed   []string  `json:"failed,omitempty"`
	At       time.Time `json:"at"`
}

// newestRun is the first of reports, newest first, that is task's: its
// goblin named it, or it ran the commit the task's worktree or its pull
// request is at.
func newestRun(reports []verify.Report, task Task) (verify.Report, bool) {
	for _, report := range reports {
		if report.Task == task.ID || report.Commit != "" && (report.Commit == task.Head || task.HostedChecks != nil && report.Commit == task.HostedChecks.Head) {
			return report, true
		}
	}
	return verify.Report{}, false
}

// localChecks is what a run's report says, for a card.
func localChecks(report verify.Report) *LocalChecks {
	checks := &LocalChecks{Commit: report.Commit, Level: report.Level, Required: report.RequiredLevel, Status: report.Status, Seconds: report.DurationSeconds, Waited: report.QueueSeconds, At: report.Start}
	for _, check := range report.Checks {
		named := false
		for _, result := range check.Packages {
			if result.Status != "passed" && result.Status != "no_tests" {
				checks.Failed = append(checks.Failed, shortPackage(result.Package, report.Project))
				named = true
			}
		}
		if !named && check.Status != "passed" && check.Status != "not_run" && len(check.Command) >= 2 {
			checks.Failed = append(checks.Failed, strings.Join(check.Command[:2], " "))
		}
	}
	return checks
}

// shortPackage is an import path from its project's folder on, such as
// internal/supervisor, and the path unchanged when it does not name it.
func shortPackage(importPath, project string) string {
	if _, rest, found := strings.Cut(importPath, "/"+project+"/"); found {
		return rest
	}
	return importPath
}

// readLocalReports reads, by project, the cfo gate test reports of each
// project a live task works in.
func (s *Service) readLocalReports() (map[string][]verify.Report, error) {
	read := s.Options.VerifyReports
	if read == nil {
		return nil, nil
	}
	reports := map[string][]verify.Report{}
	var errs error
	for _, meta := range liveTasks(s.Store.Home.State) {
		project := filepath.Base(meta.Project)
		if _, done := reports[project]; done {
			continue
		}
		found, err := read(project)
		errs = errors.Join(errs, err)
		reports[project] = found
	}
	return reports, errs
}

// localChecksLog is the end of the log of the run a task's card shows, and
// whether one is kept. A log is read only from its project's folder of the
// report store, where cfo gate test writes it.
func (s *Service) localChecksLog(meta state.TaskMeta) ([]string, bool, error) {
	evaluation := s.Store.Snapshot().Tasks[meta.ID]
	project := filepath.Base(meta.Project)
	s.mu.Lock()
	reports := s.localReports[project]
	hosted, isHosted := s.hostedChecks[evaluation.PR]
	s.mu.Unlock()
	task := Task{ID: meta.ID, Evaluation: evaluation}
	if isHosted && evaluation.PR != "" {
		task.HostedChecks = &hosted
	}
	report, found := newestRun(reports, task)
	if !found || filepath.Ext(report.Log) != ".log" || filepath.Base(filepath.Dir(report.Log)) != project {
		return nil, false, nil
	}
	lines, err := fileTail(report.Log, 200)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	return lines, err == nil, err
}
