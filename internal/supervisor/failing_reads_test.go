package supervisor

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/home"
)

// timedOutGit is a goblin's worktree whose reads run out of time while
// isTimingOut says so.
type timedOutGit struct {
	progressGit
	isTimingOut *bool
}

func (git *timedOutGit) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	if *git.isTimingOut {
		return execx.Result{}, context.DeadlineExceeded
	}
	return git.progressGit.Run(ctx, request)
}

// On 2026-10-10, with every processor busy, the supervisor woke the CFO twice
// in minutes with "progress for <task>: context deadline exceeded" for three
// goblins at a time, and once with "gh could not read <pull request>: context
// deadline exceeded". Each of those reads is made again on the next pass, so
// one that failed once tells nothing of the goblin or of the supervisor. The
// CFO is woken once the read failed on failingPasses passes in a row, and a
// pass that reads it ends the run.
func TestAReadThatFailedWakesTheCFOOnlyOnceItKeepsFailing(t *testing.T) {
	const pull = "https://github.com/o/r/pull/7"
	timedOut := fmt.Sprintf("gh could not read %s: %v", pull, context.DeadlineExceeded)
	pullRead := func(isTimingOut *bool) func(context.Context, string) (PullRequestInfo, error) {
		return func(context.Context, string) (PullRequestInfo, error) {
			if *isTimingOut {
				return PullRequestInfo{}, fmt.Errorf("gh could not read %s: %w", pull, context.DeadlineExceeded)
			}
			return PullRequestInfo{State: "OPEN", Title: "Ship it"}, nil
		}
	}
	cases := []struct {
		name, told string
		arrange    func(t *testing.T, h home.Home, service *Service, isTimingOut *bool)
	}{
		{"a goblin's progress", "progress for busy-task: context deadline exceeded", func(t *testing.T, h home.Home, service *Service, isTimingOut *bool) {
			liveGoblin(t, h, "busy-task", h.Root)
			head := strings.Repeat("a", 40)
			service.Options.Progress = &timedOutGit{progressGit: progressGit{head: head, pushed: head}, isTimingOut: isTimingOut}
		}},
		{"the pull request a paused goblin waits on", "pause condition for pr-task: " + timedOut, func(t *testing.T, h home.Home, service *Service, isTimingOut *bool) {
			pausedGoblin(t, h, "pr-task", "dependency", "pr:"+pull, time.Now().UTC().Add(-time.Hour))
			service.Options.PullRequestState = pullRead(isTimingOut)
		}},
		{"the pull request a queued row waits on", "the pull request next-task waits on, " + pull + ": " + timedOut, func(t *testing.T, h home.Home, service *Service, isTimingOut *bool) {
			queueBriefedTask(t, h, "- **next-task** - Ship it (repo: code-goblins) blocked-by: "+pull+" - after it merges", plainBrief)
			service.Options.PullRequestState = pullRead(isTimingOut)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			handler, h := startBoard(t, 8*gigabyte, &spawnRecorder{})
			service := handler.Service
			service.work, service.subscribers = make(chan struct{}, 1), map[chan struct{}]struct{}{}
			isTimingOut := false
			c.arrange(t, h, service, &isTimingOut)
			start := time.Now().UTC().Truncate(time.Minute)

			for pass, step := range []struct {
				isTimingOut bool
				wakes       int
			}{{true, 0}, {false, 0}, {true, 0}, {true, 0}, {true, 1}} {
				isTimingOut = step.isTimingOut

				// Act
				_ = service.checkFleet(t.Context(), start.Add(time.Duration(pass)*time.Minute))
				service.cycle(t.Context(), true)

				// Assert
				var wakes []string
				for _, detail := range supervisorErrorWakes(t, service) {
					if strings.Contains(detail, c.told) {
						wakes = append(wakes, detail)
					}
				}
				if len(wakes) != step.wakes {
					t.Fatalf("after pass %d (timing out: %t) the CFO had %d wakes naming %q, want %d: %q", pass+1, step.isTimingOut, len(wakes), c.told, step.wakes, supervisorErrorWakes(t, service))
				}
			}
		})
	}
}
