package supervisor

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// awaitedTaskRunning records other-task as a live goblin of the home, so a
// row that waits on it waits on a task the home knows.
func awaitedTaskRunning(t *testing.T, h home.Home) {
	t.Helper()
	if err := state.WriteTaskMeta(h.State, state.TaskMeta{ID: "other-task", Backend: "native", SpawnGen: "generation-other-task"}); err != nil {
		t.Fatal(err)
	}
}

// awaitedTaskDelivered records that other-task delivered.
func awaitedTaskDelivered(t *testing.T, h home.Home) {
	t.Helper()
	if err := state.WriteOutcome(h.State, state.Outcome{ID: "other-task", Generation: "generation-other-task", Phase: "done", Reason: "delivered", Evidence: "https://github.com/o/r/pull/9", At: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
}

// pullRequestNow makes the forge say the pull request a row waits on is in
// pullState.
func pullRequestNow(service *Service, pullState string) {
	service.Options.PullRequestState = func(context.Context, string) (PullRequestInfo, error) {
		return PullRequestInfo{State: pullState}, nil
	}
}

// The Overlord, 2026-10-09: "it should pick up paused tasks and new tasks
// automatically". A queued row says what it waits for in words the scheduler
// reads, and once that clears the scheduler starts the row at its next
// reading with memory free, with no edit to the row: a time once it passes,
// free memory once a reading has that much, a task once its outcome says it
// delivered, a pull request once it merged. Until then the row does not
// start.
func TestTheSchedulerStartsARowOnceWhatItWaitsForClears(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	cases := []struct {
		name, blocker string
		available     uint64
		arrange       func(t *testing.T, h home.Home, service *Service)
		isCleared     bool
	}{
		{"a time still ahead", "until " + now.Add(time.Hour).Format(time.RFC3339), 8 * gigabyte, func(*testing.T, home.Home, *Service) {}, false},
		{"a time that passed", "until " + now.Add(-time.Minute).Format("2006-01-02T15:04Z07:00"), 8 * gigabyte, func(*testing.T, home.Home, *Service) {}, true},
		{"memory not yet free", "memory 12 GB", 8 * gigabyte, func(*testing.T, home.Home, *Service) {}, false},
		{"memory free", "memory 12 GB", 13 * gigabyte, func(*testing.T, home.Home, *Service) {}, true},
		{"a task still at work", "other-task", 8 * gigabyte, func(t *testing.T, h home.Home, _ *Service) { awaitedTaskRunning(t, h) }, false},
		{"a task that delivered", "other-task", 8 * gigabyte, func(t *testing.T, h home.Home, _ *Service) { awaitedTaskDelivered(t, h) }, true},
		{"a pull request still open", "https://github.com/o/r/pull/7", 8 * gigabyte, func(_ *testing.T, _ home.Home, service *Service) { pullRequestNow(service, "OPEN") }, false},
		{"a pull request that merged", "https://github.com/o/r/pull/7", 8 * gigabyte, func(_ *testing.T, _ home.Home, service *Service) { pullRequestNow(service, "MERGED") }, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			spawner := &spawnRecorder{}
			handler, h := startBoard(t, c.available, spawner)
			queueBriefedTask(t, h, "- **next-task** - Ship it (repo: code-goblins) blocked-by: "+c.blocker+" - the reason", plainBrief)
			c.arrange(t, h, handler.Service)

			// Act
			if err := handler.Service.checkFleet(t.Context(), now); err != nil {
				t.Fatal(err)
			}
			starts := 0
			if c.isCleared {
				starts = 1
			}
			calls := awaitCalls(t, spawner, starts)
			awaitSettled(t, handler.Service)

			// Assert
			isStarted := slices.ContainsFunc(calls, func(call []string) bool { return len(call) > 1 && call[0] == "spawn" && call[1] == "next-task" })
			if isStarted != c.isCleared {
				t.Fatalf("dispatches=%v, want next-task started %t", calls, c.isCleared)
			}
		})
	}
}

// A row whose wait the scheduler cannot read keeps waiting, never starts,
// and says why in plain words: on its card, and as work that waits, which
// the CFO hears of. A task the home has never heard of is one, as the
// quiet-night and fleet-slots rows were, which sat while memory was free.
func TestARowWhoseWaitCannotBeReadKeepsWaitingAndSaysWhy(t *testing.T) {
	cases := []struct{ name, blocker, why string }{
		{"a task the home never heard of", "quiet-night", `No task is named "quiet-night"`},
		{"a time it cannot read", "until tomorrow", `Cannot read "until tomorrow" as a time`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			spawner := &spawnRecorder{}
			handler, h := startBoard(t, 16*gigabyte, spawner)
			queueBriefedTask(t, h, "- **next-task** - Ship it (repo: code-goblins) blocked-by: "+c.blocker+" - needs 12 GB free for a minute", plainBrief)

			// Act
			if err := handler.Service.checkFleet(t.Context(), time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			calls := awaitCalls(t, spawner, 0)
			card := cardOf(t, handler, "next-task")
			snapshot, err := handler.Service.Snapshot()

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if len(calls) != 0 {
				t.Fatalf("dispatches=%v, want nothing started", calls)
			}
			if len(card.Waits) != 1 || card.Waits[0].Problem != c.why {
				t.Errorf("card waits = %+v, want one wait saying %q", card.Waits, c.why)
			}
			if snapshot.Scheduling == nil || !strings.Contains(snapshot.Scheduling.Text, "next-task: "+c.why) {
				t.Errorf("scheduling = %+v, want next-task named as waiting, with %q", snapshot.Scheduling, c.why)
			}
		})
	}
}

// A waiting card carries what its row still waits for, for its words, and
// nothing that has cleared: a row whose waits all cleared carries none.
func TestAWaitingCardCarriesWhatItStillWaitsFor(t *testing.T) {
	// Arrange
	spawner := &spawnRecorder{}
	handler, h := startBoard(t, 8*gigabyte, spawner)
	writeFile(t, filepath.Join(h.Data, "backlog.md"), "## Queued\n"+
		"- **next-task** - Ship it (repo: code-goblins) blocked-by: until 2000-01-01T00:00Z blocked-by: memory 12 GB blocked-by: other-task - after it\n"+
		"- **cleared-task** - Ship that (repo: code-goblins) blocked-by: until 2000-01-01T00:00Z - after the reset\n")
	writeFile(t, filepath.Join(h.Data, "next-task", "brief.md"), plainBrief)
	writeFile(t, filepath.Join(h.Data, "cleared-task", "brief.md"), plainBrief)
	awaitedTaskRunning(t, h)

	// Act
	waiting, cleared := cardOf(t, handler, "next-task"), cardOf(t, handler, "cleared-task")

	// Assert
	want := []fleet.Blocker{{Kind: "memory", Target: "memory 12 GB", Bytes: 12 << 30}, {Kind: "task", Target: "other-task"}}
	if !slices.Equal(waiting.Waits, want) {
		t.Errorf("waiting card waits = %+v, want %+v", waiting.Waits, want)
	}
	if len(cleared.Waits) != 0 {
		t.Errorf("cleared card waits = %+v, want none", cleared.Waits)
	}
}

// A pull request a row waits on whose state cannot be read keeps that row
// waiting, and nothing else: the scheduler still says why other work waits,
// and the CFO is still woken for it.
func TestAPullRequestThatCannotBeReadHoldsOnlyItsOwnRow(t *testing.T) {
	// Arrange
	spawner := &spawnRecorder{}
	handler, h := startBoard(t, 8*gigabyte, spawner)
	writeFile(t, filepath.Join(h.Data, "backlog.md"), "## Queued\n"+
		"- **pr-task** - Ship after it (repo: code-goblins) blocked-by: https://github.com/o/r/pull/7 - after it merges\n"+
		"- **night-task** - Quiet night work (repo: code-goblins) blocked-by: quiet-night - needs 12 GB free\n")
	writeFile(t, filepath.Join(h.Data, "pr-task", "brief.md"), plainBrief)
	writeFile(t, filepath.Join(h.Data, "night-task", "brief.md"), plainBrief)
	handler.Service.Options.PullRequestState = func(context.Context, string) (PullRequestInfo, error) {
		return PullRequestInfo{}, errors.New("gh: could not resolve to a PullRequest")
	}
	start := time.Now().UTC().Truncate(time.Minute)

	// Act
	for minute := range 32 {
		_ = handler.Service.checkFleet(t.Context(), start.Add(time.Duration(minute)*time.Minute))
	}

	// Assert
	if calls := spawner.recorded(); len(calls) != 0 {
		t.Fatalf("dispatches=%v, want nothing started", calls)
	}
	wakes := fleetWakeRecords(t, h, "idle")
	if len(wakes) != 1 || !strings.Contains(wakes[0].Detail, `night-task: No task is named "quiet-night"`) {
		t.Fatalf("idle wakes %v, want one naming night-task and why", wakes)
	}
}

// A row whose wait can never clear says why, as one the scheduler cannot
// read does: a task that stopped without delivering, or a pull request
// closed without merging, would otherwise hold it forever unseen.
func TestARowWhoseWaitCanNeverClearSaysWhy(t *testing.T) {
	cases := []struct {
		name, blocker, why string
		arrange            func(t *testing.T, h home.Home, service *Service)
	}{
		{"a task that stopped", "other-task", "other-task stopped without delivering", func(t *testing.T, h home.Home, _ *Service) {
			if err := state.WriteOutcome(h.State, state.Outcome{ID: "other-task", Generation: "generation-other-task", Phase: "stopped", Reason: "stopped by the CFO", At: time.Now().UTC()}); err != nil {
				t.Fatal(err)
			}
		}},
		{"a pull request closed", "https://github.com/o/r/pull/7", "PR #7 closed without merging", func(_ *testing.T, _ home.Home, service *Service) { pullRequestNow(service, "CLOSED") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			spawner := &spawnRecorder{}
			handler, h := startBoard(t, 8*gigabyte, spawner)
			queueBriefedTask(t, h, "- **next-task** - Ship it (repo: code-goblins) blocked-by: "+c.blocker+" - after it", plainBrief)
			c.arrange(t, h, handler.Service)

			// Act
			if err := handler.Service.checkFleet(t.Context(), time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			calls := awaitCalls(t, spawner, 0)
			card := cardOf(t, handler, "next-task")
			snapshot, err := handler.Service.Snapshot()

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if len(calls) != 0 {
				t.Fatalf("dispatches=%v, want nothing started", calls)
			}
			if len(card.Waits) != 1 || card.Waits[0].Problem != c.why {
				t.Errorf("card waits = %+v, want one wait saying %q", card.Waits, c.why)
			}
			if snapshot.Scheduling == nil || !strings.Contains(snapshot.Scheduling.Text, "next-task: "+c.why) {
				t.Errorf("scheduling = %+v, want next-task named as waiting, with %q", snapshot.Scheduling, c.why)
			}
		})
	}
}
