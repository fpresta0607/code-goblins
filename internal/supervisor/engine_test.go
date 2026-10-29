package supervisor

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

type engineGate struct {
	isRunning bool
	err       error
}

func TestCFOEngineSelectionSavesWithoutRestartAndRejectsInvalidChoices(t *testing.T) {
	handler, h := startBoard(t, 8*gigabyte, &spawnRecorder{})
	machine := newFirstRunMachine(t)
	machine.runs = true
	handler.Service.Options.FirstRun = machine.run
	if err := os.WriteFile(filepath.Join(machine.home, ".codex", "models_cache.json"), []byte(`{"models":[{"slug":"selected-model","visibility":"list","supported_reasoning_levels":[{"effort":"high"}]}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	response := taskControlRequest(handler, "/api/cfo/engine", map[string]string{"harness": "codex", "model": "selected-model", "effort": "high"})
	if response.Code != http.StatusOK || machine.restarted != 0 {
		t.Fatalf("save = %d %s, restarted %d", response.Code, response.Body, machine.restarted)
	}
	path := filepath.Join(h.State, "cfo-engine.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []map[string]string{
		{"harness": "codex", "model": "missing-model", "effort": "high"},
		{"harness": "codex", "model": "selected-model", "effort": "max"},
		{"harness": "unknown", "model": "selected-model", "effort": "high"},
		{"harness": "codex", "model": "default", "effort": "high"},
	} {
		response = taskControlRequest(handler, "/api/cfo/engine", invalid)
		after, err := os.ReadFile(path)
		if response.Code != http.StatusConflict || err != nil || string(after) != string(before) || machine.restarted != 0 {
			t.Fatalf("invalid save %v = %d %s, file %s, err %v, restarted %d", invalid, response.Code, response.Body, after, err, machine.restarted)
		}
	}
}

// The catalog is the harness's own, so a selection it offers is one the reader
// must hand that harness even where the fleet's shared list has no such word.
func TestReadCFOEngineHandsAHarnessItsOwnModelAndEffort(t *testing.T) {
	for _, test := range []struct {
		name     string
		saved    string
		wantErr  bool
		wantModl string
	}{
		{name: "an effort only its own catalog offers", saved: `{"harness":"claude","model":"claude-opus-5-5","effort":"ultra"}`, wantModl: "claude-opus-5-5"},
		{name: "an effort as a switch", saved: `{"harness":"codex","model":"gpt-6-astra","effort":"--yolo"}`, wantErr: true},
		{name: "no effort", saved: `{"harness":"codex","model":"gpt-6-astra"}`, wantErr: true},
		{name: "no model", saved: `{"harness":"codex","effort":"xhigh"}`, wantErr: true},
		{name: "a harness the fleet cannot start", saved: `{"harness":"grok","model":"grok-4","effort":"high"}`, wantErr: true},
		{name: "the catalog's word for whichever model", saved: `{"harness":"codex","model":"default","effort":"high"}`, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "cfo-engine.json"), []byte(test.saved), 0o600); err != nil {
				t.Fatal(err)
			}
			choice, err := ReadCFOEngine(dir)
			if test.wantErr {
				if err == nil || !strings.Contains(err.Error(), "cfo-engine.json") {
					t.Fatalf("choice=%+v err=%v, want a refusal naming the file", choice, err)
				}
				return
			}
			if err != nil || choice.Model != test.wantModl || choice.Effort != "ultra" {
				t.Fatalf("choice=%+v err=%v", choice, err)
			}
		})
	}
}

func (gate *engineGate) Progress(context.Context, string, string) (pipeline.Progress, error) {
	if gate.err != nil {
		return pipeline.Progress{}, gate.err
	}
	status := "completed"
	if gate.isRunning {
		status = "running"
	}
	return pipeline.Progress{Steps: []pipeline.ProgressStep{{Name: "review", Status: status}}}, nil
}

func (*engineGate) CanSteer(context.Context, string, string, string) error { return nil }

func TestEngineSelectionSavesQueuedSettingsForStartAndRejectsStaleEdits(t *testing.T) {
	handler, h := startBoard(t, 5*gigabyte, &spawnRecorder{})
	handler.Service.Options.FirstRun = newFirstRunMachine(t).run
	queueBriefedTask(t, h, "- **next-task** - Ship it (repo: project)", plainBrief)
	before, err := fleet.ReadQueuedTask(h, "next-task")
	if err != nil {
		t.Fatal(err)
	}
	input := map[string]string{"task": "next-task", "revision": before.Revision, "harness": "codex", "model": "default", "effort": "high"}

	response := taskControlRequest(handler, "/api/tasks/engine", input)

	if response.Code != 200 {
		t.Fatalf("queued choice = %d: %s", response.Code, response.Body)
	}
	plan, err := planStart(h, "next-task", readFinishedWork(h, nil), rowReading{now: time.Now().UTC()})
	if err != nil || plan.harness != "codex" || plan.model != "default" || plan.effort != "high" {
		t.Fatalf("Start settings = %+v, %v", plan, err)
	}
	if response := taskControlRequest(handler, "/api/tasks/engine", input); response.Code != 409 {
		t.Fatalf("stale choice = %d: %s", response.Code, response.Body)
	}
}

func TestQueuedEngineSelectionPreservesAutomaticAndPartialPreferencesThroughStart(t *testing.T) {
	for _, choice := range []state.EngineChoice{
		{},
		{Harness: "codex"},
		{Model: "provider/model"},
		{Effort: "high"},
		{Harness: "codex", Model: "default"},
		{Harness: "codex", Effort: "high"},
		{Model: "provider/model", Effort: "high"},
		{Harness: "codex", Model: "default", Effort: "high"},
	} {
		t.Run(choice.Harness+"/"+choice.Model+"/"+choice.Effort, func(t *testing.T) {
			spawner := &spawnRecorder{}
			handler, h := startBoard(t, 8*gigabyte, spawner)
			handler.Service.Options.FirstRun = newFirstRunMachine(t).run
			brief := plainBrief + "\nharness: claude\nmodel: old-model\neffort: xhigh\n"
			queueBriefedTask(t, h, "- **next-task** - Ship it (repo: project)", brief)
			before, err := fleet.ReadQueuedTask(h, "next-task")
			if err != nil {
				t.Fatal(err)
			}

			response := taskControlRequest(handler, "/api/tasks/engine", map[string]string{"task": "next-task", "revision": before.Revision, "harness": choice.Harness, "model": choice.Model, "effort": choice.Effort})

			if response.Code != 200 {
				t.Fatalf("queued choice = %d: %s", response.Code, response.Body)
			}
			card := engineCard(t, handler.Service, "next-task")
			if card.Harness != choice.Harness || card.Model != choice.Model || card.Effort != choice.Effort {
				t.Fatalf("saved snapshot restored brief preferences: %+v, want %+v", card, choice)
			}
			data, err := os.ReadFile(filepath.Join(h.Data, "next-task", "brief.md"))
			if err != nil || string(data) != brief {
				t.Fatalf("saved choice changed brief: %q, %v", data, err)
			}
			response = postStart(handler, `{"task":"next-task"}`, "board.local", "http://board.local", orderToken)
			if response.Code != 202 {
				t.Fatalf("start = %d: %s", response.Code, response.Body)
			}
			waitStarted(t, handler, "next-task")
			calls := spawner.recorded()
			if len(calls) != 1 {
				t.Fatalf("dispatches = %+v", calls)
			}
			for _, setting := range [][2]string{{"--harness", choice.Harness}, {"--model", choice.Model}, {"--effort", choice.Effort}} {
				index := slices.Index(calls[0], setting[0])
				if setting[1] == "" && index != -1 || setting[1] != "" && (index < 0 || index+1 >= len(calls[0]) || calls[0][index+1] != setting[1]) {
					t.Errorf("dispatch %v did not preserve %v", calls[0], setting)
				}
			}
		})
	}
}

func TestQueuedEngineSelectionRejectsInvalidPreferencesBeforeSaving(t *testing.T) {
	for _, choice := range []state.EngineChoice{
		{Harness: "unknown"},
		{Harness: "pi"},
		{Harness: "codex", Model: "missing-model"},
		{Model: "bad model"},
		{Model: "bad, effort: high"},
		{Effort: "banana"},
	} {
		t.Run(choice.Harness+"/"+choice.Model+"/"+choice.Effort, func(t *testing.T) {
			handler, h := startBoard(t, 8*gigabyte, &spawnRecorder{})
			handler.Service.Options.FirstRun = newFirstRunMachine(t).run
			queueBriefedTask(t, h, "- **next-task** - Ship it (repo: project)", plainBrief)
			before, err := fleet.ReadQueuedTask(h, "next-task")
			if err != nil {
				t.Fatal(err)
			}

			response := taskControlRequest(handler, "/api/tasks/engine", map[string]string{"task": "next-task", "revision": before.Revision, "harness": choice.Harness, "model": choice.Model, "effort": choice.Effort})

			if response.Code != 409 {
				t.Fatalf("invalid choice = %d: %s", response.Code, response.Body)
			}
			after, err := fleet.ReadQueuedTask(h, "next-task")
			if err != nil || after.Revision != before.Revision {
				t.Fatalf("invalid choice changed queue: %+v, %v", after, err)
			}
		})
	}
}

func TestLiveEngineSelectionStillRequiresCompleteCatalogChoices(t *testing.T) {
	for _, phase := range []string{"running", "paused"} {
		t.Run(phase, func(t *testing.T) {
			handler, h := startBoard(t, 8*gigabyte, &spawnRecorder{})
			handler.Service.Options.FirstRun = newFirstRunMachine(t).run
			meta := state.TaskMeta{ID: "task", SpawnGen: "s1", Harness: "codex", Backend: "native"}
			if err := state.WriteTaskMeta(h.State, meta); err != nil {
				t.Fatal(err)
			}
			if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: meta.ID, Generation: meta.SpawnGen, Operation: "operation", Action: "pause", Phase: phase}); err != nil {
				t.Fatal(err)
			}
			for _, choice := range []state.EngineChoice{{}, {Harness: "codex"}, {Harness: "codex", Model: "default"}} {
				response := taskControlRequest(handler, "/api/tasks/engine", map[string]string{"task": meta.ID, "generation": meta.SpawnGen, "harness": choice.Harness, "model": choice.Model, "effort": choice.Effort, "when": "turn-end"})
				if response.Code != 409 {
					t.Fatalf("incomplete live choice = %d: %s", response.Code, response.Body)
				}
			}
			if _, err := state.ReadEngineChoice(h.State, meta.ID); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("incomplete choice was recorded: %v", err)
			}
		})
	}
}

func TestQueuedEngineSelectionRejectsLiveActionsBeforeSaving(t *testing.T) {
	for _, action := range []string{"update", "now", "turn-end", "cancel"} {
		t.Run(action, func(t *testing.T) {
			handler, h := startBoard(t, 8*gigabyte, &spawnRecorder{})
			handler.Service.Options.FirstRun = newFirstRunMachine(t).run
			queueBriefedTask(t, h, "- **next-task** - Ship it (repo: project)", plainBrief)
			before, err := fleet.ReadQueuedTask(h, "next-task")
			if err != nil {
				t.Fatal(err)
			}

			response := taskControlRequest(handler, "/api/tasks/engine", map[string]string{"task": "next-task", "revision": before.Revision, "when": action, "effort": "banana"})

			if response.Code != 400 {
				t.Fatalf("queued live action = %d: %s", response.Code, response.Body)
			}
			after, err := fleet.ReadQueuedTask(h, "next-task")
			if err != nil || after.Revision != before.Revision {
				t.Fatalf("queued live action changed preferences: %+v, %v", after, err)
			}
		})
	}
}

func TestSnapshotReflectsChangedEngineBriefAndPendingChoice(t *testing.T) {
	store, h := testStore(t)
	service := &Service{Store: store}
	queueBriefedTask(t, h, "- **next-task** - Ship it (repo: project)", plainBrief+"\nharness: codex\nmodel: first-model\neffort: high\n")
	meta := state.TaskMeta{ID: "live-task", SpawnGen: "s1", Harness: "claude", Backend: "native"}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	if card := engineCard(t, service, "next-task"); card.Model != "first-model" {
		t.Fatalf("initial queued engine = %+v", card)
	}
	if card := engineCard(t, service, meta.ID); card.PendingEngine != nil {
		t.Fatalf("unexpected initial choice = %+v", card.PendingEngine)
	}

	writeFile(t, filepath.Join(h.Data, "next-task", "brief.md"), plainBrief+"\nharness: codex\nmodel: changed-model\neffort: high\n")
	choice := state.EngineChoice{ID: meta.ID, Generation: meta.SpawnGen, Harness: "codex", Model: "chosen-model", Effort: "high", When: "turn-end"}
	if err := state.WriteEngineChoice(h.State, choice); err != nil {
		t.Fatal(err)
	}

	if card := engineCard(t, service, "next-task"); card.Model != "changed-model" {
		t.Fatalf("changed queued engine = %+v", card)
	}
	if card := engineCard(t, service, meta.ID); card.PendingEngine == nil || card.PendingEngine.Model != choice.Model {
		t.Fatalf("saved choice = %+v", card.PendingEngine)
	}
	if err := state.RemoveEngineChoice(h.State, meta.ID); err != nil {
		t.Fatal(err)
	}
	if card := engineCard(t, service, meta.ID); card.PendingEngine != nil {
		t.Fatalf("removed choice still visible = %+v", card.PendingEngine)
	}
}

func TestDeferredEngineSwitchWaitsForTheTurnAndGateThenSwitchesOnce(t *testing.T) {
	spawner := &spawnRecorder{}
	handler, h := startBoard(t, 5*gigabyte, spawner)
	s := handler.Service
	s.Options.FirstRun = newFirstRunMachine(t).run
	isIdle := false
	s.Options.Dispatch.Idle = func(context.Context, state.TaskMeta) (bool, error) { return isIdle, nil }
	gate := &engineGate{}
	s.Options.Gate = gate
	meta := state.TaskMeta{ID: "task", SpawnGen: "s1", Harness: "claude", Model: defaultModel, Effort: "xhigh", Backend: "native", Worktree: h.Root, Project: h.Root}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	gitFixture(t, meta.Worktree)
	input := map[string]string{"task": meta.ID, "generation": meta.SpawnGen, "harness": "codex", "model": "default", "effort": "high", "when": "turn-end"}
	response := taskControlRequest(handler, "/api/tasks/engine", input)
	if response.Code != 202 {
		t.Fatalf("deferred choice = %d: %s", response.Code, response.Body)
	}
	now := time.Now().UTC()
	if err := s.applyEngineChoices(context.Background(), now); err != nil || len(spawner.recorded()) != 0 {
		t.Fatalf("mid-turn switch = %v, calls %+v", err, spawner.recorded())
	}
	isIdle, gate.isRunning = true, true
	if err := s.applyEngineChoices(context.Background(), now.Add(time.Second)); err != nil || len(spawner.recorded()) != 0 {
		t.Fatalf("active gate switch = %v, calls %+v", err, spawner.recorded())
	}
	gate.isRunning = false
	for _, elapsed := range []time.Duration{2 * time.Second, 2500 * time.Millisecond} {
		if err := s.applyEngineChoices(context.Background(), now.Add(elapsed)); err != nil || len(spawner.recorded()) != 0 {
			t.Fatalf("single idle reading switched = %v, calls %+v", err, spawner.recorded())
		}
	}
	if err := s.applyEngineChoices(context.Background(), now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Minute)
	for len(spawner.recorded()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	calls := spawner.recorded()
	if len(calls) != 1 || calls[0][0] != "switch" {
		t.Fatalf("idle switch calls = %+v", calls)
	}
	for _, elapsed := range []time.Duration{4 * time.Second, 5 * time.Second} {
		if err := s.applyEngineChoices(context.Background(), now.Add(elapsed)); err != nil {
			t.Fatal(err)
		}
	}
	if len(spawner.recorded()) != 1 {
		t.Fatalf("deferred choice applied twice: %+v", spawner.recorded())
	}
	for time.Now().Before(deadline) {
		s.starts.Lock()
		isChanging := s.changing[meta.ID] != ""
		s.starts.Unlock()
		if !isChanging {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("deferred switch did not finish reporting its outcome")
}

func TestEngineSelectionRefusesUnavailableModelsAndCompletedTasks(t *testing.T) {
	handler, _ := startBoard(t, 5*gigabyte, &spawnRecorder{})
	handler.Service.Options.FirstRun = newFirstRunMachine(t).run
	for _, input := range []map[string]string{
		{"task": "task", "harness": "pi", "model": "default", "effort": "high"},
		{"task": "task", "harness": "codex", "model": "missing-model", "effort": "high"},
		{"task": "finished:task", "harness": "codex", "model": "default", "effort": "high"},
	} {
		response := taskControlRequest(handler, "/api/tasks/engine", input)
		if response.Code < 400 {
			t.Fatalf("invalid choice accepted: %+v: %d", input, response.Code)
		}
	}
}

func TestEngineSelectionSavesPausedChoiceAndCancelsIt(t *testing.T) {
	handler, h := startBoard(t, 5*gigabyte, &spawnRecorder{})
	handler.Service.Options.FirstRun = newFirstRunMachine(t).run
	meta := state.TaskMeta{ID: "task", SpawnGen: "s1", Harness: "claude", Backend: "native"}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: meta.ID, Generation: meta.SpawnGen, Operation: "pause-1", Action: "pause", Phase: "paused"}); err != nil {
		t.Fatal(err)
	}
	input := map[string]string{"task": meta.ID, "generation": meta.SpawnGen, "harness": "codex", "model": "default", "effort": "high"}
	response := taskControlRequest(handler, "/api/tasks/engine", input)
	if response.Code != 200 {
		t.Fatalf("paused choice = %d: %s", response.Code, response.Body)
	}
	choice, err := state.ReadEngineChoice(h.State, meta.ID)
	if err != nil || choice.When != "resume" || choice.Harness != "codex" {
		t.Fatalf("Resume choice = %+v, %v", choice, err)
	}
	input["when"] = "cancel"
	if response := taskControlRequest(handler, "/api/tasks/engine", input); response.Code != 200 {
		t.Fatalf("cancel choice = %d: %s", response.Code, response.Body)
	}
	if _, err := state.ReadEngineChoice(h.State, meta.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled choice = %v", err)
	}
}

func TestQueuedSnapshotNamesTheEngineThatStartWillUse(t *testing.T) {
	handler, h := startBoard(t, 5*gigabyte, &spawnRecorder{})
	queueBriefedTask(t, h, "- **next-task** - Ship it (repo: project, harness: pi, model: provider/model, effort: high)", plainBrief)
	plan, err := planStart(h, "next-task", readFinishedWork(h, nil), rowReading{now: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}

	snapshot, err := handler.Service.Snapshot()

	if err != nil {
		t.Fatalf("snapshot = %+v, %v", snapshot, err)
	}
	var task Task
	for _, item := range snapshot.Tasks {
		if item.ID == "next-task" {
			task = item
		}
	}
	if task.Harness != plan.harness || task.Model != plan.model || task.Effort != plan.effort {
		t.Fatalf("card engine = %s %s %s, Start = %+v", task.Harness, task.Model, task.Effort, plan)
	}
}

func TestCompletedSnapshotKeepsRecordedEngineThroughStoppedLifecycle(t *testing.T) {
	_, h := startBoard(t, 5*gigabyte, &spawnRecorder{})
	writeFile(t, filepath.Join(h.State, "outcomes", "task.json"), `{"id":"task","generation":"s1","title":"Task","phase":"stopped","harness":"codex","model":"recorded-model","effort":"xhigh","at":"`+time.Now().UTC().Format(time.RFC3339)+`"}`)
	if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: "task", Generation: "s1", Operation: "stop-1", Action: "stop", Phase: "stopped", Updated: time.Now()}); err != nil {
		t.Fatal(err)
	}

	tasks := finishedTasks(h, &keptReads{}, time.Now())

	if len(tasks) != 1 || tasks[0].Harness != "codex" || tasks[0].Model != "recorded-model" || tasks[0].Effort != "xhigh" {
		t.Fatalf("completed engine was lost: %+v", tasks)
	}
}

func waitEngineChange(t *testing.T, service *Service, id string) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for time.Now().Before(deadline) {
		service.starts.Lock()
		isChanging := service.changing[id] != ""
		service.starts.Unlock()
		if !isChanging {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("engine switch did not finish")
}

func TestDeferredEngineContinuesOtherTasksAndRequiresFreshIdleEvidenceAfterErrors(t *testing.T) {
	spawner := &spawnRecorder{}
	handler, h := startBoard(t, 5*gigabyte, spawner)
	service := handler.Service
	service.Options.FirstRun = newFirstRunMachine(t).run
	service.Options.Dispatch.Idle = func(context.Context, state.TaskMeta) (bool, error) { return true, nil }
	gate := &engineGate{}
	service.Options.Gate = gate
	gitFixture(t, h.Root)
	meta := state.TaskMeta{ID: "task", SpawnGen: "s1", Harness: "claude", Backend: "native", Worktree: h.Root, Project: h.Root}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteEngineChoice(h.State, state.EngineChoice{ID: meta.ID, Generation: meta.SpawnGen, Harness: "codex", Model: "default", Effort: "high", When: "turn-end", Requested: time.Now()}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(h.State, "engine", "aaa.json"), "invalid json")
	now := time.Now()
	if err := service.applyEngineChoices(context.Background(), now); err == nil {
		t.Fatal("malformed choice was hidden")
	}
	service.starts.Lock()
	_, hasReading := service.engineIdle[meta.ID]
	service.starts.Unlock()
	if !hasReading {
		t.Fatal("one malformed choice prevented the other task from progressing")
	}
	if err := os.Remove(filepath.Join(h.State, "engine", "aaa.json")); err != nil {
		t.Fatal(err)
	}
	gate.err = errors.New("gate cannot be read")
	// stall-accepted: a time handed to the engine pass, which compares it only with the times it was handed before.
	if err := service.applyEngineChoices(context.Background(), now.Add(time.Second)); err != nil {
		t.Fatalf("gate error reached the board: %v", err)
	}
	if card := engineCard(t, service, meta.ID); card.ActionError != gate.err.Error() || card.PendingEngine == nil {
		t.Fatalf("gate error card = %q, pending %+v", card.ActionError, card.PendingEngine)
	}
	gate.err = nil
	// stall-accepted: a time handed to the engine pass, which compares it only with the times it was handed before.
	if err := service.applyEngineChoices(context.Background(), now.Add(2*time.Second)); err != nil || len(spawner.recorded()) != 0 {
		t.Fatalf("stale idle evidence switched = %v, %+v", err, spawner.recorded())
	}
	// stall-accepted: a time handed to the engine pass, which compares it only with the times it was handed before.
	if err := service.applyEngineChoices(context.Background(), now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	waitEngineChange(t, service, meta.ID)
	if len(spawner.recorded()) != 1 {
		t.Fatalf("fresh idle evidence did not switch: %+v", spawner.recorded())
	}
}

func engineCard(t *testing.T, service *Service, id string) Task {
	t.Helper()
	snapshot, err := service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Error != "" {
		t.Fatalf("board error = %q", snapshot.Error)
	}
	for _, task := range snapshot.Tasks {
		if task.ID == id {
			return task
		}
	}
	t.Fatalf("task %s is missing from %+v", id, snapshot.Tasks)
	return Task{}
}

func TestDeferredEngineFailuresStayOnTheirCardsAndUnavailableChoicesAreRemovedOnce(t *testing.T) {
	spawner := &spawnRecorder{}
	handler, h := startBoard(t, 5*gigabyte, spawner)
	service := handler.Service
	service.Options.FirstRun = newFirstRunMachine(t).run
	idleErr := errors.New("screen cannot be read")
	service.Options.Dispatch.Idle = func(_ context.Context, meta state.TaskMeta) (bool, error) {
		if meta.ID == "unreadable" {
			return false, idleErr
		}
		return true, nil
	}
	service.Options.Gate = &engineGate{}
	gitFixture(t, h.Root)
	choices := map[string]string{"unavailable": "missing-model", "unreadable": "default", "other": "default"}
	for id, model := range choices {
		meta := state.TaskMeta{ID: id, SpawnGen: "s1", Harness: "claude", Backend: "native", Worktree: h.Root, Project: h.Root}
		if err := state.WriteTaskMeta(h.State, meta); err != nil {
			t.Fatal(err)
		}
		if err := state.WriteEngineChoice(h.State, state.EngineChoice{ID: id, Generation: meta.SpawnGen, Harness: "codex", Model: model, Effort: "high", When: "turn-end", Requested: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()

	for _, elapsed := range []time.Duration{0, time.Second, 2 * time.Second, 3 * time.Second, 4 * time.Second} {
		// stall-accepted: each is a time handed to the engine pass, which compares it only with the times it was handed before.
		if err := service.applyEngineChoices(context.Background(), now.Add(elapsed)); err != nil {
			t.Fatalf("engine choice failure reached the board: %v", err)
		}
	}
	waitEngineChange(t, service, "other")
	calls := spawner.recorded()
	if len(calls) != 1 || calls[0][0] != "switch" || calls[0][1] != "other" {
		t.Fatalf("switch calls = %+v", calls)
	}
	if _, err := state.ReadEngineChoice(h.State, "unavailable"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unavailable choice was kept: %v", err)
	}
	if card := engineCard(t, service, "unavailable"); card.ActionError != "Model missing-model is unavailable for Codex" || card.PendingEngine != nil {
		t.Fatalf("unavailable card = %q, pending %+v", card.ActionError, card.PendingEngine)
	}
	if _, err := state.ReadEngineChoice(h.State, "unreadable"); err != nil {
		t.Fatalf("idle-read failure removed the choice: %v", err)
	}
	if card := engineCard(t, service, "unreadable"); card.ActionError != idleErr.Error() || card.PendingEngine == nil {
		t.Fatalf("unreadable card = %q, pending %+v", card.ActionError, card.PendingEngine)
	}
	pending, err := wake.Pending(h.State)
	if err != nil {
		t.Fatal(err)
	}
	digests := map[string]int{}
	for _, record := range pending {
		if record.Kind == "notify" {
			digests[record.Key]++
		}
		if record.Key == "unreadable" {
			t.Fatalf("idle-read failure told the CFO: %+v", record)
		}
	}
	for _, id := range []string{"unavailable", "other"} {
		if digests[id] != 1 {
			t.Fatalf("%s choice digests = %d in %+v", id, digests[id], pending)
		}
	}
}

func TestDeferredEngineIdleReadFailureClearsAfterRecoveryOrCancel(t *testing.T) {
	for _, isCancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "recovered while busy", true: "cancelled"}[isCancelled], func(t *testing.T) {
			handler, h := startBoard(t, 5*gigabyte, &spawnRecorder{})
			service := handler.Service
			service.Options.FirstRun = newFirstRunMachine(t).run
			idleErr := errors.New("screen cannot be read")
			readErr := idleErr
			service.Options.Dispatch.Idle = func(context.Context, state.TaskMeta) (bool, error) { return false, readErr }
			service.Options.Gate = &engineGate{}
			meta := state.TaskMeta{ID: "task", SpawnGen: "s1", Harness: "claude", Backend: "native", Worktree: h.Root, Project: h.Root}
			if err := state.WriteTaskMeta(h.State, meta); err != nil {
				t.Fatal(err)
			}
			input := map[string]string{"task": meta.ID, "generation": meta.SpawnGen, "harness": "codex", "model": "default", "effort": "high", "when": "turn-end"}
			if response := taskControlRequest(handler, "/api/tasks/engine", input); response.Code != 202 {
				t.Fatalf("deferred choice = %d: %s", response.Code, response.Body)
			}
			now := time.Now()
			if err := service.applyEngineChoices(context.Background(), now); err != nil {
				t.Fatal(err)
			}
			if card := engineCard(t, service, meta.ID); card.ActionError != idleErr.Error() {
				t.Fatalf("idle-read failure card = %q", card.ActionError)
			}

			if isCancelled {
				input["when"] = "cancel"
				if response := taskControlRequest(handler, "/api/tasks/engine", input); response.Code != 200 {
					t.Fatalf("cancel choice = %d: %s", response.Code, response.Body)
				}
			} else {
				readErr = nil
				// stall-accepted: a time handed to the engine pass, which compares it only with the times it was handed before.
				if err := service.applyEngineChoices(context.Background(), now.Add(time.Second)); err != nil {
					t.Fatal(err)
				}
			}

			card := engineCard(t, service, meta.ID)
			if card.ActionError != "" || isCancelled == (card.PendingEngine != nil) {
				t.Fatalf("card after recovery = %q, pending %+v", card.ActionError, card.PendingEngine)
			}
		})
	}
}

func TestImmediateEngineSwitchKeepsOldValuesUntilCompletionAndReportsFailure(t *testing.T) {
	for _, isFailed := range []bool{false, true} {
		t.Run(map[bool]string{false: "new session", true: "failed launch"}[isFailed], func(t *testing.T) {
			spawner := &spawnRecorder{release: make(chan struct{})}
			if isFailed {
				spawner.err = errors.New("launch failed")
			}
			handler, h := startBoard(t, 5*gigabyte, spawner)
			service := handler.Service
			service.Options.FirstRun = newFirstRunMachine(t).run
			meta := state.TaskMeta{ID: "task-1", SpawnGen: "g1", Harness: "claude", Model: "old-model", Effort: "xhigh", Backend: "native", Worktree: h.Root, Project: h.Root}
			if err := state.WriteTaskMeta(h.State, meta); err != nil {
				t.Fatal(err)
			}
			response := taskControlRequest(handler, "/api/tasks/engine", map[string]string{"task": meta.ID, "generation": meta.SpawnGen, "harness": "codex", "model": "default", "effort": "high", "when": "now"})
			if response.Code != 202 {
				close(spawner.release)
				t.Fatalf("immediate choice = %d: %s", response.Code, response.Body)
			}
			defer func() { close(spawner.release); waitEngineChange(t, service, meta.ID) }()
			if !isFailed {
				meta.SpawnGen, meta.Harness, meta.Model, meta.Effort = "g2", "codex", "default", "high"
				if err := state.WriteTaskMeta(h.State, meta); err != nil {
					t.Fatal(err)
				}
			}

			snapshot, err := service.Snapshot()

			if err != nil || len(snapshot.Tasks) == 0 || !snapshot.Tasks[0].Switching || snapshot.Tasks[0].Harness != "claude" || snapshot.Tasks[0].Model != "old-model" || snapshot.Tasks[0].Effort != "xhigh" || snapshot.Tasks[0].Phase == "" {
				t.Fatalf("unfinished switch exposed new settings: %+v, %v", snapshot.Tasks, err)
			}
			spawner.release <- struct{}{}
			waitEngineChange(t, service, meta.ID)
			snapshot, err = service.Snapshot()
			if err != nil || snapshot.Tasks[0].Switching {
				t.Fatalf("completed switch = %+v, %v", snapshot.Tasks, err)
			}
			if isFailed && snapshot.Tasks[0].ActionError == "" || !isFailed && snapshot.Tasks[0].Harness != "codex" {
				t.Fatalf("switch outcome = %+v", snapshot.Tasks[0])
			}
		})
	}
}

func TestSuccessfulCLIActionClearsAnEarlierEngineFailure(t *testing.T) {
	for _, hasPrior := range []bool{false, true} {
		name := "no prior lifecycle"
		if hasPrior {
			name = "existing lifecycle"
		}
		t.Run(name, func(t *testing.T) {
			spawner := &spawnRecorder{err: errors.New("switch failed")}
			handler, h := startBoard(t, 5*gigabyte, spawner)
			service := handler.Service
			meta := state.TaskMeta{ID: "task-1", SpawnGen: "generation-1", Harness: "claude", Backend: "native", Worktree: h.Root, Project: h.Root}
			if err := state.WriteTaskMeta(h.State, meta); err != nil {
				t.Fatal(err)
			}
			record := state.Lifecycle{ID: meta.ID, Generation: meta.SpawnGen, Operation: "resume-1", Action: "resume", Phase: "running", Updated: time.Now().Add(-time.Minute)}
			if hasPrior {
				if err := state.WriteLifecycle(h.State, record); err != nil {
					t.Fatal(err)
				}
			}
			service.starts.Lock()
			service.startEngineSwitch(meta, state.EngineChoice{ID: meta.ID, Generation: meta.SpawnGen, Harness: "codex", Model: "default", Effort: "high"})
			service.starts.Unlock()
			waitEngineChange(t, service, meta.ID)
			for _, isRecovered := range []bool{false, true} {
				if isRecovered {
					record.Phase, record.Updated = "paused", time.Now()
					if err := state.WriteLifecycle(h.State, record); err != nil {
						t.Fatal(err)
					}
				}
				snapshot, err := service.Snapshot()
				if err != nil || len(snapshot.Tasks) != 1 {
					t.Fatalf("snapshot=%+v error=%v", snapshot.Tasks, err)
				}
				message := snapshot.Tasks[0].ActionError
				if isRecovered && message != "" || !isRecovered && message == "" {
					t.Fatalf("recovered=%t error=%q", isRecovered, message)
				}
			}
		})
	}
}

// Restart in a working goblin's Harness section restarts it at once on its
// own harness, model and effort, on its own conversation, through cfo switch
// --restart. A paused goblin has Resume for that, and a queued task has no
// session to restart.
func TestBoardRestartRestartsAGoblinOnItsOwnEngineAtOnce(t *testing.T) {
	cases := []struct {
		name       string
		phase      string
		isQueued   bool
		wantStatus int
		wantCalls  [][]string
	}{
		{name: "working", phase: "running", wantStatus: http.StatusAccepted, wantCalls: [][]string{{"switch", "goblin", "--generation", "s1", "--restart"}}},
		{name: "paused", phase: "paused", wantStatus: http.StatusConflict},
		{name: "queued", isQueued: true, wantStatus: http.StatusBadRequest},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			spawner := &spawnRecorder{}
			handler, h := startBoard(t, 5*gigabyte, spawner)
			service := handler.Service
			meta := state.TaskMeta{ID: "goblin", SpawnGen: "s1", Harness: "claude", Model: "opus", Effort: "xhigh", Backend: "native", Worktree: h.Root, Project: h.Root}
			generation := meta.SpawnGen
			if test.isQueued {
				generation = ""
			} else {
				if err := state.WriteTaskMeta(h.State, meta); err != nil {
					t.Fatal(err)
				}
				if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: meta.ID, Generation: meta.SpawnGen, Operation: "op-1", Action: "resume", Phase: test.phase, Updated: time.Now()}); err != nil {
					t.Fatal(err)
				}
			}

			// Act
			response := taskControlRequest(handler, "/api/tasks/engine", map[string]string{"task": meta.ID, "generation": generation, "when": "restart"})
			if response.Code == http.StatusAccepted {
				waitEngineChange(t, service, meta.ID)
			}

			// Assert
			if response.Code != test.wantStatus || !slices.EqualFunc(spawner.recorded(), test.wantCalls, slices.Equal[[]string]) {
				t.Fatalf("restart = %d %s, calls %+v; want %d and %+v", response.Code, response.Body, spawner.recorded(), test.wantStatus, test.wantCalls)
			}
			if _, err := state.ReadEngineChoice(h.State, meta.ID); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("engine choice after restart: %v; want none left", err)
			}
		})
	}
}
