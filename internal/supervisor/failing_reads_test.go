package supervisor

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
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

// timedOutOrigin is a forge whose read of a repository's origin runs out of
// time while isTimingOut says so.
type timedOutOrigin struct {
	*fakeForge
	isTimingOut *bool
}

func (forge timedOutOrigin) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	if *forge.isTimingOut && slices.Equal(request.Args, []string{"config", "--get", "remote.origin.url"}) {
		return execx.Result{}, context.DeadlineExceeded
	}
	return forge.fakeForge.Run(ctx, request)
}

// timedOutTrainOrigin is a forge whose origin the CI poll reads, and whose
// origin then runs out of time when the merge train reads it again later in
// the same poll, while isTimingOut says so.
type timedOutTrainOrigin struct {
	*fakeForge
	isTimingOut *bool
	hasListed   bool
}

func (forge *timedOutTrainOrigin) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	command := request.Name + " " + strings.Join(request.Args, " ")
	if command == "git config --get remote.origin.url" {
		isTrains := forge.hasListed
		forge.hasListed = false
		if isTrains && *forge.isTimingOut {
			return execx.Result{}, context.DeadlineExceeded
		}
	}
	if strings.HasPrefix(command, "gh pr list") {
		forge.hasListed = true
	}
	return forge.fakeForge.Run(ctx, request)
}

// failedBefore records that each read failed on every pass a read that keeps
// failing needs but the last, so its next failure is told.
func failedBefore(t *testing.T, h home.Home, reads ...string) {
	t.Helper()
	watched := fleetWakes{Failing: map[string]int{}}
	for _, read := range reads {
		watched.Failing[read] = failingPasses - 1
	}
	if err := writeFleetWakes(h.State, watched); err != nil {
		t.Fatal(err)
	}
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
	// Each arranges one read that runs out of time while isTimingOut says
	// so, and returns the line the supervisor's error names it by.
	cases := []struct {
		name    string
		arrange func(t *testing.T, h home.Home, service *Service, isTimingOut *bool) string
	}{
		{"a goblin's progress", func(t *testing.T, h home.Home, service *Service, isTimingOut *bool) string {
			liveGoblin(t, h, "busy-task", h.Root)
			head := strings.Repeat("a", 40)
			service.Options.Progress = &timedOutGit{progressGit: progressGit{head: head, pushed: head}, isTimingOut: isTimingOut}
			return "progress for busy-task: context deadline exceeded"
		}},
		{"the pull request a paused goblin waits on", func(t *testing.T, h home.Home, service *Service, isTimingOut *bool) string {
			pausedGoblin(t, h, "pr-task", "dependency", "pr:"+pull, time.Now().UTC().Add(-time.Hour))
			service.Options.PullRequestState = pullRead(isTimingOut)
			return "pause condition for pr-task: " + timedOut
		}},
		{"the pull request a queued row waits on", func(t *testing.T, h home.Home, service *Service, isTimingOut *bool) string {
			queueBriefedTask(t, h, "- **next-task** - Ship it (repo: code-goblins) blocked-by: "+pull+" - after it merges", plainBrief)
			service.Options.PullRequestState = pullRead(isTimingOut)
			return "the pull request next-task waits on, " + pull + ": " + timedOut
		}},
		{"the origin of a repository a goblin works in", func(t *testing.T, h home.Home, service *Service, isTimingOut *bool) string {
			liveGoblin(t, h, "ci-task", h.Root)
			forge := forgeFor(h.Root, "ci-task")
			forge.branch, forge.pulls, forge.runs = "feat/wakes", "[]", "[]"
			service.Options.CI = timedOutOrigin{fakeForge: forge, isTimingOut: isTimingOut}
			return "ci wakes: read the origin of " + filepath.Clean(h.Root) + ": context deadline exceeded"
		}},
		{"the origin a merge train reads of a repository a goblin works in", func(t *testing.T, h home.Home, service *Service, isTimingOut *bool) string {
			liveGoblin(t, h, "train-task", h.Root)
			forge := forgeFor(h.Root, "train-task")
			forge.branch, forge.pulls, forge.runs = "feat/wakes", "[]", "[]"
			service.Options.CI = &timedOutTrainOrigin{fakeForge: forge, isTimingOut: isTimingOut}
			return "merge train: read the origin of " + filepath.Clean(h.Root) + ": context deadline exceeded"
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			handler, h := startBoard(t, 8*gigabyte, &spawnRecorder{})
			service := handler.Service
			service.work, service.subscribers = make(chan struct{}, 1), map[chan struct{}]struct{}{}
			isTimingOut := false
			told := c.arrange(t, h, service, &isTimingOut)
			start := time.Now().UTC().Truncate(time.Minute)

			for pass, step := range []struct {
				isTimingOut bool
				wakes       int
			}{{true, 0}, {false, 0}, {true, 0}, {true, 0}, {true, 1}} {
				isTimingOut = step.isTimingOut

				// Act
				_ = service.checkFleet(t.Context(), start.Add(time.Duration(pass)*ciPollEvery))
				service.cycle(t.Context(), true)

				// Assert
				var wakes []string
				for _, detail := range supervisorErrorWakes(t, service) {
					if strings.Contains(detail, told) {
						wakes = append(wakes, detail)
					}
				}
				if len(wakes) != step.wakes {
					t.Fatalf("after pass %d (timing out: %t) the CFO had %d wakes naming %q, want %d: %q", pass+1, step.isTimingOut, len(wakes), told, step.wakes, supervisorErrorWakes(t, service))
				}
			}
		})
	}
}
