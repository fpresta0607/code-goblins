package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/quota"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func pausedGoblin(t *testing.T, h home.Home, id, reason, until string, at time.Time) state.TaskMeta {
	t.Helper()
	meta := state.TaskMeta{ID: id, SpawnGen: "generation-" + id, Backend: "native", Project: h.Root, Worktree: filepath.Join(h.Root, ".worktrees", "gb-"+id), TaskTmp: filepath.Join(h.State, "tasktmp", id)}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	condition, err := state.NewPauseCondition(reason, until, at)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: id, Generation: meta.SpawnGen, Operation: "pause-" + id, Action: "pause", Phase: "paused", Started: at, Updated: at, Pause: &condition, Reason: reason}); err != nil {
		t.Fatal(err)
	}
	return meta
}

func TestAllowanceFloorRequestsPauseWithTheResetCondition(t *testing.T) {
	for _, percentUsed := range []float64{94.99, 95, 95.01, 96, 96.9, 97, 98} {
		t.Run(fmt.Sprintf("%.2f used", percentUsed), func(t *testing.T) {
			spawner := &spawnRecorder{}
			handler, h := startBoard(t, 3*gigabyte, spawner)
			now := time.Now().UTC().Truncate(time.Second)
			meta := state.TaskMeta{ID: "running-task", Backend: "native", Harness: "claude", SpawnGen: "generation-1"}
			if err := state.WriteTaskMeta(h.State, meta); err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(host.Record{ID: meta.ID, HostPID: os.Getpid(), Started: now.Add(time.Second), Pipe: "fixture", Token: "fixture"})
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(h.State, "hosts", meta.ID+".json"), string(data))
			reset := now.Add(time.Hour)
			handler.Service.Options.Quota = func(context.Context) (quota.Report, string) {
				return quota.Report{Providers: map[string]quota.Provider{"claude": {Known: true, Windows: []quota.Window{{ID: "week", Kind: "weekly", PercentUsed: percentUsed, ResetsAt: reset}}, Scopes: map[string]quota.Scope{"all_models": {Name: "all_models", Known: true, PercentRemaining: 100 - percentUsed, ResetsAt: reset, BoundedBy: []string{"week"}}}}}}, ""
			}

			if err := handler.Service.checkFleet(t.Context(), now); err != nil {
				t.Fatal(err)
			}

			if percentUsed >= 95 {
				calls := awaitDispatch(t, handler.Service, spawner, 1)
				if calls[0][0] != "pause" || calls[0][1] != meta.ID || !strings.Contains(strings.Join(calls[0], " "), "--reason allowance --until "+reset.Format(time.RFC3339)) {
					t.Fatalf("allowance pause=%v", calls)
				}
			} else if calls := spawner.recorded(); len(calls) != 0 {
				t.Fatalf("paused above the floor: %v", calls)
			}
		})
	}
}

func TestFailedAllowancePauseIsHeldOnlyForItsGenerationAndReset(t *testing.T) {
	for _, test := range []struct {
		name, failedGeneration, action, task string
		resetOffset                          time.Duration
	}{
		{"same failed attempt", "generation-1", "resume", "ready-task", 0},
		{"previous generation", "generation-0", "pause", "failed-floor", 0},
		{"previous reset", "generation-1", "pause", "failed-floor", -time.Minute},
	} {
		t.Run(test.name, func(t *testing.T) {
			spawner := &spawnRecorder{}
			handler, h := startBoard(t, 8*gigabyte, spawner)
			now := time.Now().UTC().Truncate(time.Second)
			reset := now.Add(time.Hour)
			meta := state.TaskMeta{ID: "failed-floor", Backend: "native", Harness: "claude", SpawnGen: "generation-1"}
			if err := state.WriteTaskMeta(h.State, meta); err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(host.Record{ID: meta.ID, HostPID: os.Getpid(), Started: now.Add(time.Second), Pipe: "fixture", Token: "fixture"})
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(h.State, "hosts", meta.ID+".json"), string(data))
			condition, err := state.NewPauseCondition("allowance", reset.Add(test.resetOffset).Format(time.RFC3339), now)
			if err != nil {
				t.Fatal(err)
			}
			if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: meta.ID, Generation: test.failedGeneration, Operation: "failed-pause", Action: "pause", Phase: "failed", Pause: &condition}); err != nil {
				t.Fatal(err)
			}
			pausedGoblin(t, h, "ready-task", "dependency", "date:"+now.Add(-time.Minute).Format(time.RFC3339), now.Add(-time.Hour))
			handler.Service.Options.Quota = func(context.Context) (quota.Report, string) {
				return quota.Report{Providers: map[string]quota.Provider{"claude": {Known: true, Windows: []quota.Window{{ID: "week", Kind: "weekly", PercentUsed: 98, ResetsAt: reset}}, Scopes: map[string]quota.Scope{"all_models": {Known: true, PercentRemaining: 2, ResetsAt: reset, BoundedBy: []string{"week"}}}}}}, ""
			}

			if err := handler.Service.checkFleet(t.Context(), now); err != nil {
				t.Fatal(err)
			}

			calls := awaitDispatch(t, handler.Service, spawner, 1)
			if calls[0][0] != test.action || calls[0][1] != test.task {
				t.Fatalf("dispatches=%v, want %s for %s", calls, test.action, test.task)
			}
		})
	}
}

func awaitDispatch(t *testing.T, service *Service, spawner *spawnRecorder, count int) [][]string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		service.starts.Lock()
		isChanging := service.starting != "" || len(service.changing) > 0
		service.starts.Unlock()
		if calls := spawner.recorded(); len(calls) >= count && !isChanging {
			return calls
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("dispatches=%v, want %d", spawner.recorded(), count)
	return nil
}

func TestMemoryPauseResumesOnlyAfterTwoReadingsOfMemoryAndCommit(t *testing.T) {
	spawner := &spawnRecorder{}
	handler, h := startBoard(t, 8*gigabyte, spawner)
	now := time.Now().UTC()
	meta := pausedGoblin(t, h, "memory-task", "memory", "", now.Add(-time.Hour))
	meter := &memoryReadings{readings: [][2]float64{{4, 8}, {8, 4}, {8, 8}, {8, 8}}}
	handler.Service.Options.Dispatch.Memory = meter.read

	for reading := 0; reading < 4; reading++ {
		if err := handler.Service.checkFleet(t.Context(), now.Add(time.Duration(reading)*time.Minute)); err != nil {
			t.Fatal(err)
		}
		if reading < 3 && len(spawner.recorded()) != 0 {
			t.Fatalf("resumed before two good readings: %v", spawner.recorded())
		}
	}

	calls := awaitDispatch(t, handler.Service, spawner, 1)
	if len(calls) != 1 || calls[0][0] != "resume" || calls[0][1] != meta.ID {
		t.Fatalf("dispatches=%v, want in-place resume", calls)
	}
	retained, err := state.ReadTaskMeta(h.State, meta.ID)
	if err != nil || retained.Worktree != meta.Worktree || retained.SpawnGen != meta.SpawnGen {
		t.Fatalf("resume discarded task identity: %+v %v", retained, err)
	}
}

func TestClearedPauseRunsBeforeQueueAndDatesDoNotHoldTheSlot(t *testing.T) {
	for _, testCase := range []struct {
		name, reason, until, command, id string
	}{
		{name: "cleared memory before queue", reason: "memory", command: "resume", id: "paused-task"},
		{name: "future date permits queue", reason: "dependency", until: "date:2099-01-01T00:00:00Z", command: "spawn", id: "next-task"},
		{name: "Overlord pause permits queue", reason: "overlord", command: "spawn", id: "next-task"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			spawner := &spawnRecorder{}
			handler, h := startBoard(t, 8*gigabyte, spawner)
			now := time.Now().UTC()
			pausedGoblin(t, h, "paused-task", testCase.reason, testCase.until, now.Add(-time.Hour))
			queueBriefedTask(t, h, "- **next-task** - Ship it", plainBrief)
			meter := &memoryReadings{readings: [][2]float64{{8, 8}, {8, 8}}}
			handler.Service.Options.Dispatch.Memory = meter.read
			if testCase.reason == "memory" {
				if err := writeFleetWakes(h.State, fleetWakes{Schema: fleetWakesSchema, MemoryAbove: 1, MemoryReadAt: now.Add(-time.Minute)}); err != nil {
					t.Fatal(err)
				}
			}

			if err := handler.Service.checkFleet(t.Context(), now); err != nil {
				t.Fatal(err)
			}

			calls := awaitDispatch(t, handler.Service, spawner, 1)
			if calls[0][0] != testCase.command || calls[0][1] != testCase.id {
				t.Fatalf("first dispatch=%v, want %s %s", calls, testCase.command, testCase.id)
			}
		})
	}
}

func TestFirstGoodMemoryReadingHoldsTheQueueForAMemoryPause(t *testing.T) {
	spawner := &spawnRecorder{}
	handler, h := startBoard(t, 8*gigabyte, spawner)
	now := time.Now().UTC()
	pausedGoblin(t, h, "paused-task", "memory", "", now.Add(-time.Hour))
	queueBriefedTask(t, h, "- **next-task** - Ship it", plainBrief)
	meter := &memoryReadings{readings: [][2]float64{{8, 8}, {8, 8}}}
	handler.Service.Options.Dispatch.Memory = meter.read

	if err := handler.Service.checkFleet(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	handler.Service.starts.Lock()
	isChanging := handler.Service.starting != "" || len(handler.Service.changing) > 0
	handler.Service.starts.Unlock()
	if calls := spawner.recorded(); len(calls) != 0 || isChanging {
		t.Fatalf("first good reading dispatched %v (changing %t)", calls, isChanging)
	}
	if err := handler.Service.checkFleet(t.Context(), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	calls := awaitDispatch(t, handler.Service, spawner, 1)
	if calls[0][0] != "resume" || calls[0][1] != "paused-task" {
		t.Fatalf("first dispatch=%v, want the memory pause resumed before the queue", calls)
	}
}

func TestPRPauseResumesOnlyWhenTheNamedPRMerged(t *testing.T) {
	for _, phase := range []string{"OPEN", "CLOSED", "MERGED"} {
		t.Run(phase, func(t *testing.T) {
			spawner := &spawnRecorder{}
			handler, h := startBoard(t, 8*gigabyte, spawner)
			now := time.Now().UTC()
			pull := "https://github.com/owner/project/pull/42"
			pausedGoblin(t, h, "pr-task", "dependency", "pr:"+pull, now.Add(-time.Hour))
			handler.Service.Options.PullRequestState = func(_ context.Context, url string) (PullRequestInfo, error) {
				if url != pull {
					t.Fatalf("read unrelated PR %s", url)
				}
				return PullRequestInfo{State: phase}, nil
			}

			if err := handler.Service.checkFleet(t.Context(), now); err != nil {
				t.Fatal(err)
			}

			if phase == "MERGED" {
				calls := awaitDispatch(t, handler.Service, spawner, 1)
				if calls[0][0] != "resume" || calls[0][1] != "pr-task" {
					t.Fatalf("dispatches=%v", calls)
				}
			} else if calls := spawner.recorded(); len(calls) != 0 {
				t.Fatalf("%s PR resumed: %v", phase, calls)
			}
		})
	}
}

func TestAllowancePauseResumesAtItsResetAndTaskPauseRequiresDelivery(t *testing.T) {
	for _, testCase := range []struct {
		name, reason, phase    string
		isBefore, shouldResume bool
	}{
		{name: "before reset", reason: "allowance", isBefore: true},
		{name: "at reset", reason: "allowance", shouldResume: true},
		{name: "missing task", reason: "dependency"},
		{name: "stopped task", reason: "dependency", phase: "stopped"},
		{name: "delivered task", reason: "dependency", phase: "done", shouldResume: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			spawner := &spawnRecorder{}
			handler, h := startBoard(t, 8*gigabyte, spawner)
			now := time.Now().UTC().Truncate(time.Second)
			until := "task:dependency-task"
			if testCase.reason == "allowance" {
				reset := now
				if testCase.isBefore {
					reset = reset.Add(time.Second)
				}
				until = reset.Format(time.RFC3339)
			}
			pausedGoblin(t, h, "waiting-task", testCase.reason, until, now.Add(-time.Hour))
			if testCase.phase != "" {
				if err := state.WriteOutcome(h.State, state.Outcome{ID: "dependency-task", Phase: testCase.phase, Evidence: "merged PR"}); err != nil {
					t.Fatal(err)
				}
			}

			if err := handler.Service.checkFleet(t.Context(), now); err != nil {
				t.Fatal(err)
			}

			if testCase.shouldResume {
				if calls := awaitDispatch(t, handler.Service, spawner, 1); calls[0][0] != "resume" || calls[0][1] != "waiting-task" {
					t.Fatalf("dispatch=%v", calls)
				}
			} else if calls := spawner.recorded(); len(calls) != 0 {
				t.Fatalf("resumed before condition cleared: %v", calls)
			}
		})
	}
}

func TestOverlordsStartWinsOverClearedPause(t *testing.T) {
	spawner := &spawnRecorder{}
	handler, h := startBoard(t, 8*gigabyte, spawner)
	pausedGoblin(t, h, "paused-task", "dependency", "date:2000-01-01T00:00:00Z", time.Now().Add(-time.Hour))
	queueBriefedTask(t, h, "- **next-task** - Ship it", plainBrief)

	response := postStart(handler, `{"task":"next-task"}`, "board.local", "http://board.local", orderToken)

	if response.Code != 202 {
		t.Fatalf("Start refused: %d %s", response.Code, response.Body)
	}
	calls := awaitDispatch(t, handler.Service, spawner, 1)
	if calls[0][0] != "spawn" || calls[0][1] != "next-task" {
		t.Fatalf("Overlord's choice lost: %v", calls)
	}
	waitStarted(t, handler, "next-task")
}

func TestOldestClearedPauseResumesFirst(t *testing.T) {
	spawner := &spawnRecorder{}
	handler, h := startBoard(t, 8*gigabyte, spawner)
	now := time.Now().UTC()
	pausedGoblin(t, h, "a-newer-task", "dependency", "date:2000-01-01T00:00:00Z", now.Add(-time.Hour))
	pausedGoblin(t, h, "z-older-task", "dependency", "date:2000-01-01T00:00:00Z", now.Add(-2*time.Hour))

	if err := handler.Service.checkFleet(t.Context(), now); err != nil {
		t.Fatal(err)
	}

	calls := awaitDispatch(t, handler.Service, spawner, 1)
	if calls[0][1] != "z-older-task" || strings.Contains(strings.Join(calls[0], " "), "spawn") {
		t.Fatalf("oldest pause lost: %v", calls)
	}
}

func TestUnreadableDependencyDoesNotHoldTheQueue(t *testing.T) {
	spawner := &spawnRecorder{}
	handler, h := startBoard(t, 8*gigabyte, spawner)
	now := time.Now().UTC()
	pausedGoblin(t, h, "pr-task", "dependency", "pr:https://github.com/owner/repo/pull/42", now.Add(-time.Hour))
	queueBriefedTask(t, h, "- **next-task** - Ship it", plainBrief)
	handler.Service.Options.PullRequestState = func(context.Context, string) (PullRequestInfo, error) {
		return PullRequestInfo{}, errors.New("GitHub is unavailable")
	}

	err := handler.Service.checkFleet(t.Context(), now)

	if err == nil || !strings.Contains(err.Error(), "GitHub is unavailable") {
		t.Fatalf("read failure hidden: %v", err)
	}
	if calls := awaitDispatch(t, handler.Service, spawner, 1); calls[0][0] != "spawn" || calls[0][1] != "next-task" {
		t.Fatalf("dependency held the queue: %v", calls)
	}
}

func TestReportedProductionDefectJumpsClearedPausesAndQueue(t *testing.T) {
	spawner := &spawnRecorder{}
	handler, h := startBoard(t, 8*gigabyte, spawner)
	now := time.Now().UTC()
	pausedGoblin(t, h, "paused-task", "dependency", "date:2000-01-01T00:00:00Z", now.Add(-time.Hour))
	queueBriefedTask(t, h, "- **next-task** - Ship it\n- **urgent-task** - Repair production (priority: production-defect)", plainBrief)
	writeFile(t, filepath.Join(h.Data, "urgent-task", "brief.md"), strings.ReplaceAll(plainBrief, "next-task", "urgent-task"))

	if err := handler.Service.checkFleet(t.Context(), now); err != nil {
		t.Fatal(err)
	}

	if calls := awaitDispatch(t, handler.Service, spawner, 1); calls[0][0] != "spawn" || calls[0][1] != "urgent-task" {
		t.Fatalf("production defect did not jump: %v", calls)
	}
	for _, report := range fleetWakeRecords(t, h, "notify") {
		if strings.Contains(report.Detail, "production defect jumped the order") {
			return
		}
	}
	t.Fatal("board report did not explain the ordering exception")
}

func TestSnapshotMarksTheQueuedProductionDefectForTheBoard(t *testing.T) {
	// Arrange
	handler, h := startBoard(t, 8*gigabyte, &spawnRecorder{})
	queueBriefedTask(t, h, "- **next-task** - Ship it\n- **urgent-task** - Repair production (priority: production-defect)", plainBrief)
	writeFile(t, filepath.Join(h.Data, "urgent-task", "brief.md"), strings.ReplaceAll(plainBrief, "next-task", "urgent-task"))

	// Act
	snapshot, err := handler.Service.Snapshot()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	priorities := map[string]string{}
	for _, task := range snapshot.Tasks {
		priorities[task.ID] = task.Priority
	}
	if want := map[string]string{"task-1": "", "next-task": "", "urgent-task": "production-defect"}; !maps.Equal(priorities, want) {
		t.Fatalf("priorities=%v, want %v", priorities, want)
	}
}

func TestFailedAutomaticStartDoesNotHoldTheSlotOrRetryEveryReading(t *testing.T) {
	spawner := &spawnRecorder{}
	handler, h := startBoard(t, 8*gigabyte, spawner)
	now := time.Now().UTC()
	pausedGoblin(t, h, "paused-task", "dependency", "date:2000-01-01T00:00:00Z", now.Add(-time.Hour))
	queueBriefedTask(t, h, "- **next-task** - Ship it\n- **urgent-task** - Repair production (priority: production-defect)", plainBrief)
	writeFile(t, filepath.Join(h.Data, "urgent-task", "brief.md"), strings.ReplaceAll(plainBrief, "next-task", "urgent-task"))
	handler.Service.Options.Dispatch.Spawn = func(ctx context.Context, args []string) (string, error) {
		output, err := spawner.spawn(ctx, args)
		if args[0] == "spawn" {
			return "refused: the project overlaps a running goblin", errors.New("exit status 1")
		}
		return output, err
	}

	for reading := 0; reading < 2; reading++ {
		if err := handler.Service.checkFleet(t.Context(), now.Add(time.Duration(reading)*time.Minute)); err != nil {
			t.Fatal(err)
		}
		awaitDispatch(t, handler.Service, spawner, reading+1)
	}

	calls := spawner.recorded()
	if len(calls) != 2 || calls[0][0] != "spawn" || calls[0][1] != "urgent-task" || calls[1][0] != "resume" || calls[1][1] != "paused-task" {
		t.Fatalf("dispatches=%v, want one failed defect start then the cleared pause resumed", calls)
	}
	failures := 0
	for _, report := range fleetWakeRecords(t, h, "notify") {
		if strings.HasPrefix(report.Detail, "start failed: ") {
			failures++
		}
	}
	if failures != 1 {
		t.Fatalf("start failure reported %d times, want once", failures)
	}
}
