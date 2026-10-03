package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
)

const PROGRESS_THRESHOLD = 20 * time.Minute

type WorkProgress struct {
	Generation string    `json:"generation"`
	At         time.Time `json:"at"`
	Source     string    `json:"source"`
	Seconds    int64     `json:"seconds"`
	Head       string    `json:"head,omitempty"`
	Pushed     string    `json:"pushed,omitempty"`
	Gate       string    `json:"gate,omitempty"`
	Report     string    `json:"report,omitempty"`
	Woken      bool      `json:"woken,omitempty"`
}

func (s *Service) checkProgress(ctx context.Context, watched *fleetWakes, now time.Time) error {
	if s.Options.Progress == nil {
		return nil
	}
	probe, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if watched.Progress == nil {
		watched.Progress = map[string]WorkProgress{}
	}
	var problems error
	database := s.Store.Snapshot()
	seen := map[string]bool{}
	for _, meta := range liveTasks(s.Store.Home.State) {
		seen[meta.ID] = true
		prior := watched.Progress[meta.ID]
		isNew := prior.Generation != meta.SpawnGen || prior.At.IsZero()
		if isNew {
			prior = WorkProgress{Generation: meta.SpawnGen, At: now, Source: "watch started"}
		}
		record, err := state.ReadLifecycle(s.Store.Home.State, meta.ID)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			problems = errors.Join(problems, err)
			continue
		}
		if record.Generation == meta.SpawnGen && record.SuppressesMonitoring(s.Store.Home.State) {
			watched.Progress[meta.ID] = prior
			continue
		}
		head, err := runOutput(probe, s.Options.Progress, meta.Worktree, "git", "rev-parse", "HEAD")
		if err != nil {
			problems = errors.Join(problems, fmt.Errorf("progress for %s: %w", meta.ID, err))
			continue
		}
		refs, err := runOutput(probe, s.Options.Progress, meta.Worktree, "git", "for-each-ref", "--format=%(HEAD)%09%(refname)%09%(objectname)", "refs/heads", "refs/remotes")
		if err != nil {
			problems = errors.Join(problems, fmt.Errorf("push progress for %s: %w", meta.ID, err))
			continue
		}
		branch, pushed := "", ""
		for _, line := range strings.Split(refs, "\n") {
			fields := strings.Split(line, "\t")
			if len(fields) == 3 && fields[0] == "*" {
				branch = strings.TrimPrefix(fields[1], "refs/heads/")
			}
		}
		for _, line := range strings.Split(refs, "\n") {
			fields := strings.Split(line, "\t")
			if len(fields) == 3 && branch != "" && fields[1] == "refs/remotes/origin/"+branch {
				pushed = fields[2]
			}
		}
		gate := database.Tasks[meta.ID].GateStep
		if meta.Mode == "no-mistakes" && branch != "" && s.Options.Gate != nil {
			progress, err := s.Options.Gate.Progress(probe, meta.Project, branch)
			if err != nil && !errors.Is(err, pipeline.ErrNoProgress) {
				problems = errors.Join(problems, fmt.Errorf("gate progress for %s: %w", meta.ID, err))
				continue
			}
			if err == nil {
				steps, err := json.Marshal(progress.Steps)
				if err != nil {
					return err
				}
				gate = progress.RunID + ":" + string(steps)
			}
		}
		lines, err := state.TailStatus(s.Store.Home.State, meta.ID, 200)
		if err != nil {
			problems = errors.Join(problems, err)
			continue
		}
		var reportedAt time.Time
		report := ""
		for index := len(lines) - 1; index >= 0; index-- {
			stamp, event := state.SplitStatus(lines[index])
			if born := spawnTime(meta.SpawnGen); !born.IsZero() && stamp.Before(born.Truncate(time.Second)) {
				break
			}
			if reportKind(event) != "" {
				reportedAt, report = stamp, event
				break
			}
		}
		source := ""
		if !isNew && head != prior.Head {
			source = "commit"
		} else if !isNew && pushed != prior.Pushed {
			source = "push"
		} else if !isNew && gate != prior.Gate {
			source = "gate step"
		} else if report != prior.Report && !reportedAt.IsZero() && !reportedAt.After(now) && !reportedAt.Before(prior.At) {
			source = "status report"
		}
		if source != "" {
			prior.At, prior.Source, prior.Woken = now, source, false
		}
		prior.Head, prior.Pushed, prior.Gate, prior.Report = head, pushed, gate, report
		prior.Seconds = max(0, int64(now.Sub(prior.At)/time.Second))
		if now.Sub(prior.At) >= PROGRESS_THRESHOLD && !prior.Woken {
			detail := fmt.Sprintf("progress_stalled: %s has made no new commit, push, gate step or status report for %d minutes; last progress: %s; next: inspect its work and decide whether it should pause", meta.ID, prior.Seconds/60, prior.Source)
			if err := raiseFleetWake(s.Store.Home.State, "check", meta.ID, detail); err != nil {
				problems = errors.Join(problems, err)
			} else {
				prior.Woken = true
			}
		}
		watched.Progress[meta.ID] = prior
	}
	for id := range watched.Progress {
		if !seen[id] {
			delete(watched.Progress, id)
		}
	}
	return problems
}
