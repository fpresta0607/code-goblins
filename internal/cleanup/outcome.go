package cleanup

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/worktree"
)

var deliveredPR = regexp.MustCompile(`^https://github\.com/[\w.-]+/[\w.-]+/pull/\d+$`)

func (service Service) outcome(ctx context.Context, meta state.TaskMeta, reason string) (outcome state.Outcome) {
	outcome = state.Outcome{ID: meta.ID, Generation: meta.SpawnGen, Title: meta.Title, Project: meta.Project, Harness: meta.Harness, Model: meta.Model, Effort: meta.Effort, Phase: "stopped", Reason: reason, At: time.Now().UTC()}
	if outcome.Title == "" {
		outcome.Title = meta.ID
	}
	// A helper delivers into its parent's branch, however it ended: once it
	// reported done and its parent's branch holds its work. One that never
	// reported done delivered nothing, though its parent's branch holds its
	// head when it committed nothing of its own.
	if meta.Parent != "" && service.reportedDone(meta) {
		if parent, err := state.ReadTaskMeta(service.StateDir, meta.Parent); err == nil {
			bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
			isHeld, err := (worktree.RunnerGit{Commands: service.Commands}).HoldsHead(bounded, parent.Worktree, meta.Worktree)
			cancel()
			if err == nil && isHeld {
				outcome.Phase, outcome.Evidence = "done", "merged into its parent "+meta.Parent+"'s branch"
				return outcome
			}
		}
	}
	if current, err := state.ReadLifecycle(service.StateDir, meta.ID); err == nil && current.Generation == meta.SpawnGen && current.Action == "stop" {
		defer func() { outcome.Phase, outcome.Reason = "stopped", current.Reason }()
	}
	values, err := state.ReadMeta(filepath.Join(service.StateDir, meta.ID+".meta"))
	if err == nil && deliveredPR.MatchString(values["pr"]) {
		outcome.PR, outcome.Phase, outcome.Evidence = values["pr"], "done", "recorded pull request"
		return outcome
	}
	lines, _ := state.TailStatus(service.StateDir, meta.ID, 200)
	nanoseconds, _ := strconv.ParseInt(strings.TrimPrefix(meta.SpawnGen, "s"), 10, 64)
	for index := len(lines) - 1; index >= 0; index-- {
		at, line := state.SplitStatus(lines[index])
		if nanoseconds > 0 && at.Before(time.Unix(0, nanoseconds).Truncate(time.Second)) {
			break
		}
		if pr, ok := strings.CutPrefix(line, "done: PR "); ok && deliveredPR.MatchString(strings.TrimSpace(pr)) {
			outcome.PR, outcome.Phase, outcome.Evidence = strings.TrimSpace(pr), "done", "reported pull request"
			return outcome
		}
	}
	if meta.Kind == "scout" && meta.Brief != "" {
		brief, err := fsx.ReadFile(meta.Brief)
		report := filepath.Join(filepath.Dir(meta.Brief), "report.md")
		if info, statErr := os.Stat(report); err == nil && statErr == nil && info.Mode().IsRegular() && info.Size() > 0 && strings.Contains(string(brief), "report.md") {
			outcome.Phase, outcome.Evidence = "done", "requested report.md delivered"
			return outcome
		}
	}
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	git := func(args ...string) (string, bool) {
		result, err := service.Commands.Run(bounded, execx.Request{Dir: meta.Worktree, Name: "git", Args: args})
		return strings.TrimSpace(string(result.Stdout)), err == nil && result.ExitCode == 0
	}
	branch, ok := git("symbolic-ref", "--short", "HEAD")
	if !ok || branch == "" {
		return outcome
	}
	outcome.Branch = branch
	head, ok := git("rev-parse", "HEAD")
	if !ok || head == "" {
		return outcome
	}
	remote, ok := git("ls-remote", "origin", "refs/heads/"+branch)
	fields := strings.Fields(remote)
	if !ok || len(fields) != 2 || fields[0] != head || fields[1] != "refs/heads/"+branch {
		return outcome
	}
	count, ok := git("rev-list", "--count", "origin/HEAD..HEAD")
	ahead, err := strconv.Atoi(count)
	if ok && err == nil && ahead > 0 {
		outcome.Phase, outcome.Evidence = "done", "pushed task commits "+head
	}
	return outcome
}

// reportedDone reports whether the task said it was done in its current
// generation.
func (service Service) reportedDone(meta state.TaskMeta) bool {
	lines, _ := state.TailStatus(service.StateDir, meta.ID, 200)
	nanoseconds, _ := strconv.ParseInt(strings.TrimPrefix(meta.SpawnGen, "s"), 10, 64)
	for index := len(lines) - 1; index >= 0; index-- {
		at, line := state.SplitStatus(lines[index])
		if nanoseconds > 0 && at.Before(time.Unix(0, nanoseconds).Truncate(time.Second)) {
			return false
		}
		if strings.HasPrefix(line, "done: ") {
			return true
		}
	}
	return false
}
