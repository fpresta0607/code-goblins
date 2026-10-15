package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleet"
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

// A goblin given new settings while paused comes back on them, so the
// scheduler judges its resume by the allowance of the harness it resumes on,
// never by the one it paused on.
func TestPausedGoblinResumesByTheAllowanceOfItsResumeChoice(t *testing.T) {
	for _, testCase := range []struct {
		name, reason, until, harness, chosen, chosenModel, choiceGeneration, lowProvider string
		shouldResume                                                                     bool
	}{
		{name: "chosen harness at its floor", reason: "dependency", until: "date:2000-01-01T00:00:00Z", harness: "claude", chosen: "codex", lowProvider: "codex"},
		{name: "paused harness at its floor", reason: "dependency", until: "date:2000-01-01T00:00:00Z", harness: "codex", chosen: "claude", lowProvider: "codex", shouldResume: true},
		{name: "allowance pause of the paused harness", reason: "allowance", until: "2999-01-01T00:00:00Z", harness: "claude", chosen: "codex", shouldResume: true},
		{name: "allowance pause kept on the same harness", reason: "allowance", until: "2999-01-01T00:00:00Z", harness: "claude", chosen: "claude"},
		{name: "allowance pause kept on another model of the same harness", reason: "allowance", until: "2999-01-01T00:00:00Z", harness: "claude", chosen: "claude", chosenModel: "claude-other-model"},
		{name: "choice of an earlier session", reason: "dependency", until: "date:2000-01-01T00:00:00Z", harness: "claude", chosen: "codex", choiceGeneration: "earlier-generation", lowProvider: "codex", shouldResume: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			spawner := &spawnRecorder{}
			handler, h := startBoard(t, 8*gigabyte, spawner)
			now := time.Now().UTC().Truncate(time.Second)
			meta := pausedGoblin(t, h, "chosen-task", testCase.reason, testCase.until, now.Add(-time.Hour))
			meta.Harness, meta.Model = testCase.harness, testCase.harness+"-model"
			if err := state.WriteTaskMeta(h.State, meta); err != nil {
				t.Fatal(err)
			}
			choice := state.EngineChoice{ID: meta.ID, Generation: meta.SpawnGen, Harness: testCase.chosen, Model: testCase.chosen + "-model", Effort: "high", When: "resume", Requested: now}
			if testCase.chosenModel != "" {
				choice.Model = testCase.chosenModel
			}
			if testCase.choiceGeneration != "" {
				choice.Generation = testCase.choiceGeneration
			}
			if err := state.WriteEngineChoice(h.State, choice); err != nil {
				t.Fatal(err)
			}
			handler.Service.Options.Quota = func(context.Context) (quota.Report, string) {
				providers := map[string]quota.Provider{}
				if testCase.lowProvider != "" {
					providers[testCase.lowProvider] = quota.Provider{Known: true, Windows: []quota.Window{{ID: "week", Kind: "weekly", PercentUsed: 98, ResetsAt: now.Add(time.Hour)}}, Scopes: map[string]quota.Scope{"all_models": {Name: "all_models", Known: true, PercentRemaining: 2, ResetsAt: now.Add(time.Hour), BoundedBy: []string{"week"}}}}
				}
				return quota.Report{Providers: providers}, ""
			}

			// Act
			if err := handler.Service.checkFleet(t.Context(), now); err != nil {
				t.Fatal(err)
			}

			// Assert
			if testCase.shouldResume {
				if calls := awaitDispatch(t, handler.Service, spawner, 1); calls[0][0] != "resume" || calls[0][1] != meta.ID {
					t.Fatalf("dispatch=%v, want %s resumed", calls, meta.ID)
				}
				return
			}
			handler.Service.starts.Lock()
			changing := maps.Clone(handler.Service.changing)
			handler.Service.starts.Unlock()
			if calls := spawner.recorded(); len(calls) != 0 || len(changing) != 0 {
				t.Fatalf("resumed against the allowance of its resume harness: dispatches=%v changing=%v", calls, changing)
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

// Of the paused goblins whose pauses cleared, the one highest in the
// Overlord's In progress order resumes first, however long each was paused
// and whatever its name.
func TestTheHighestClearedPauseInHisOrderResumesFirst(t *testing.T) {
	// Arrange
	spawner := &spawnRecorder{}
	handler, h := startBoard(t, 8*gigabyte, spawner)
	now := time.Now().UTC()
	pausedGoblin(t, h, "a-older-task", "dependency", "date:2000-01-01T00:00:00Z", now.Add(-2*time.Hour))
	pausedGoblin(t, h, "z-newer-task", "dependency", "date:2000-01-01T00:00:00Z", now.Add(-time.Hour))
	if err := fleet.WriteAttention(h, []string{"z-newer-task", "a-older-task"}); err != nil {
		t.Fatal(err)
	}

	// Act
	if err := handler.Service.checkFleet(t.Context(), now); err != nil {
		t.Fatal(err)
	}

	// Assert
	calls := awaitDispatch(t, handler.Service, spawner, 1)
	if calls[0][0] != "resume" || calls[0][1] != "z-newer-task" {
		t.Fatalf("dispatches = %v, want the goblin highest in his order resumed", calls)
	}
}

// A paused goblin whose pull request stays unreadable is told, and holds
// nothing else back.
func TestUnreadableDependencyDoesNotHoldTheQueue(t *testing.T) {
	spawner := &spawnRecorder{}
	handler, h := startBoard(t, 8*gigabyte, spawner)
	now := time.Now().UTC()
	pausedGoblin(t, h, "pr-task", "dependency", "pr:https://github.com/owner/repo/pull/42", now.Add(-time.Hour))
	failedBefore(t, h, "pause:pr-task")
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

// The Overlord, 2026-10-15: "shouldnt new goblins come in at lowest priority
// from queue". A task the supervisor starts by itself, a production defect
// that jumped the queue included, joins the bottom of In progress, under the
// goblins he placed and those he did not, which keep their order.
func TestAnAutomaticStartJoinsTheBottomOfHisInProgressOrder(t *testing.T) {
	for name, row := range map[string]string{
		"the next queued task":         "- **next-task** - Ship it",
		"a reported production defect": "- **next-task** - Repair production (priority: production-defect)",
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			spawner := &spawnRecorder{}
			handler, h := startBoard(t, 8*gigabyte, spawner)
			now := time.Now().UTC()
			for _, id := range []string{"his-first", "his-second", "unplaced-task"} {
				pausedGoblin(t, h, id, "overlord", "", now.Add(-time.Hour))
			}
			if err := fleet.WriteAttention(h, []string{"his-second", "his-first"}); err != nil {
				t.Fatal(err)
			}
			queueBriefedTask(t, h, row, plainBrief)

			// Act
			if err := handler.Service.checkFleet(t.Context(), now); err != nil {
				t.Fatal(err)
			}
			calls := awaitDispatch(t, handler.Service, spawner, 1)
			waitStarted(t, handler, "next-task")

			// Assert
			if calls[0][0] != "spawn" || calls[0][1] != "next-task" {
				t.Fatalf("dispatches = %v, want next-task started", calls)
			}
			if order, err := fleet.ReadAttention(h); err != nil || !slices.Equal(order, []string{"his-second", "his-first", "task-1", "unplaced-task", "next-task"}) {
				t.Fatalf("attention order = %v, %v; want his order kept and next-task at the bottom", order, err)
			}
		})
	}
}

// While a start is under way the snapshot says whether it is the Overlord's,
// so the board shows his at the top of In progress and the supervisor's own
// at the bottom, where each lands once it ends.
func TestTheSnapshotSaysWhoseStartIsUnderWay(t *testing.T) {
	for name, isOverlord := range map[string]bool{"his Start": true, "the supervisor's own": false} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			spawner := &spawnRecorder{release: make(chan struct{})}
			handler, h := startBoard(t, 8*gigabyte, spawner)
			queueBriefedTask(t, h, "- **next-task** - Ship it", plainBrief)

			// Act
			err := handler.Service.startQueued("next-task", isOverlord)
			awaitRecorded(t, spawner, 1)
			card := cardOf(t, handler, "next-task")
			close(spawner.release)
			waitStarted(t, handler, "next-task")

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if !card.Starting || card.OverlordStart != isOverlord {
				t.Fatalf("starting %v, overlord start %v; want starting and overlord start %v", card.Starting, card.OverlordStart, isOverlord)
			}
		})
	}
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

// A paused goblin whose pause cleared and whose resume is refused for what no
// later reading clears by itself, as a gate policy older than the machine's
// is, stays paused as it was. It waits for a Resume from then on, so it
// neither holds the slot ahead of the queue nor is refused again at every
// reading, and the CFO is told once. A resume that only waits for room is
// tried again at the next reading, ahead of the queue, as it always was.
func TestARefusedAutomaticResumeWaitsForAResumeAndDoesNotHoldTheSlot(t *testing.T) {
	const refusal = "resume refused, and the task is left as it was: validation did not restart: pipeline: the machine's gate config has moved on from the policy task paused-task is frozen at (agent). Run cfo pipeline migrate paused-task, then resume it"
	tests := []struct {
		name, output string
		want         []string
		wantTold     int
	}{
		{name: "refused for its gate policy", output: refusal, want: []string{"resume paused-task", "spawn next-task"}, wantTold: 1},
		{name: "waits for room", output: "waits for room: 3.1 GB of memory is free", want: []string{"resume paused-task", "resume paused-task"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			spawner := &spawnRecorder{}
			handler, h := startBoard(t, 8*gigabyte, spawner)
			now := time.Now().UTC()
			pausedGoblin(t, h, "paused-task", "dependency", "date:2000-01-01T00:00:00Z", now.Add(-time.Hour))
			queueBriefedTask(t, h, "- **next-task** - Ship it", plainBrief)
			handler.Service.Options.Dispatch.Spawn = func(ctx context.Context, args []string) (string, error) {
				output, err := spawner.spawn(ctx, args)
				if args[0] == "resume" {
					return test.output + "\n", errors.New("exit status 1")
				}
				return output, err
			}

			// Act
			for reading := range 2 {
				if err := handler.Service.checkFleet(t.Context(), now.Add(time.Duration(reading)*time.Minute)); err != nil {
					t.Fatal(err)
				}
				awaitDispatch(t, handler.Service, spawner, min(reading+1, len(test.want)))
			}

			// Assert
			var dispatched []string
			for _, call := range spawner.recorded() {
				dispatched = append(dispatched, call[0]+" "+call[1])
			}
			if !slices.Equal(dispatched, test.want) {
				t.Errorf("dispatched %q, want %q", dispatched, test.want)
			}
			told := 0
			for _, report := range fleetWakeRecords(t, h, "notify") {
				if report.Key == "paused-task" && strings.Contains(report.Detail, "cfo pipeline migrate paused-task") {
					told++
				}
			}
			if told != test.wantTold {
				t.Errorf("the CFO was told of the refusal %d times, want %d", told, test.wantTold)
			}
		})
	}
}
