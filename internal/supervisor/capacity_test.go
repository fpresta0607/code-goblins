package supervisor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/quota"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestStartPastTheConfiguredLiveCapIsRefusedWithItsReason(t *testing.T) {
	spawner := &spawnRecorder{}
	handler, h := startBoard(t, 16*gigabyte, spawner)
	queueBriefedTask(t, h, "- **next-task** - Ship it", plainBrief)
	writeFile(t, filepath.Join(h.Root, "config", "fleet.json"), `{"max_live_goblins":1}`)
	if err := state.WriteTaskMeta(h.State, state.TaskMeta{ID: "running-task", Backend: "native", SpawnGen: "generation-1"}); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(host.Record{ID: "running-task", HostPID: os.Getpid(), Started: time.Now().UTC(), Pipe: "fixture", Token: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(h.State, "hosts", "running-task.json"), string(data))

	response := postStart(handler, `{"task":"next-task"}`, "board.local", "http://board.local", orderToken)
	if response.Code == 202 {
		waitStarted(t, handler, "next-task")
	}

	if response.Code != 409 || !strings.Contains(response.Body.String(), "live goblin cap") || !strings.Contains(response.Body.String(), "1") {
		t.Fatalf("start=%d %s, want cap refusal", response.Code, response.Body)
	}
	if calls := spawner.recorded(); len(calls) != 0 {
		t.Fatalf("cap refusal dispatched %v", calls)
	}
}

func TestFleetCapacityUsesBothResourcesAndRejectsInvalidSettings(t *testing.T) {
	for _, testCase := range []struct {
		name, settings    string
		available, commit uint64
		wantSlots         int
		shouldFail        bool
	}{
		{name: "memory constrains cap", available: 6 * gigabyte, commit: 20 * gigabyte, wantSlots: 2},
		{name: "commit constrains cap", available: 20 * gigabyte, commit: 5 * gigabyte, wantSlots: 1},
		{name: "floor reserves gates", available: 4 * gigabyte, commit: 20 * gigabyte},
		{name: "setting constrains cap", settings: `{"max_live_goblins":1}`, available: 20 * gigabyte, commit: 20 * gigabyte, wantSlots: 1},
		{name: "null", settings: `null`, shouldFail: true},
		{name: "unknown key", settings: `{"maximum":4}`, shouldFail: true},
		{name: "zero maximum", settings: `{"max_live_goblins":0}`, shouldFail: true},
		{name: "two objects", settings: `{} {}`, shouldFail: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, h := fleetService(t)
			if testCase.settings != "" {
				writeFile(t, filepath.Join(h.Root, "config", "fleet.json"), testCase.settings)
			}

			capacity, err := ReadFleetCapacity(h, Memory{Available: testCase.available, CommitAvailable: testCase.commit})

			if (err != nil) != testCase.shouldFail || err == nil && capacity.Slots != testCase.wantSlots {
				t.Fatalf("capacity=%+v err=%v, want slots=%d failed=%v", capacity, err, testCase.wantSlots, testCase.shouldFail)
			}
		})
	}
}

func TestSchedulerDoesNotResumeIntoAnotherAllowancePause(t *testing.T) {
	spawner := &spawnRecorder{}
	handler, h := startBoard(t, 8*gigabyte, spawner)
	now := time.Now().UTC()
	meta := pausedGoblin(t, h, "ready-task", "dependency", "date:2000-01-01T00:00:00Z", now.Add(-time.Hour))
	meta.Harness = "codex"
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	handler.Service.Options.Quota = func(context.Context) (quota.Report, string) {
		return quota.Report{Providers: map[string]quota.Provider{"codex": {Known: true, Scopes: map[string]quota.Scope{"all_models": {Name: "all_models", Known: true, PercentRemaining: 2, ResetsAt: now.Add(time.Hour)}}}}}, ""
	}

	if err := handler.Service.checkFleet(t.Context(), now); err != nil {
		t.Fatal(err)
	}

	if calls := spawner.recorded(); len(calls) != 0 {
		t.Fatalf("resumed at the floor: %v", calls)
	}
}
