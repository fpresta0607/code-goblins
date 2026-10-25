package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lifecycle"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// boardResumeSwitch is the fixture's stand-in for the terminal launch: it
// replaces the task's generation and records the busy observation, as a real
// switch leaves them. isLocked says whether it takes the record's metadata
// lock, which production spawn.Service.Switch takes and this fake once
// omitted: without it, goblinname.Backfill's read-modify-write could restore
// the generation the switch had just replaced (#1459).
func boardResumeSwitch(stateDir string, isLocked bool) func(context.Context, home.Home, spawn.SwitchRequest) (spawn.SwitchResult, error) {
	return func(_ context.Context, _ home.Home, request spawn.SwitchRequest) (spawn.SwitchResult, error) {
		if isLocked {
			// The record's lock is exclusive and not waited for, exactly as
			// spawn.Service.Switch takes it: a naming pass holding it makes the
			// switch refuse rather than overwrite what that pass will write.
			name := state.MetadataLockName(request.ID)
			if _, err := lock.AcquireExclusiveNamed(stateDir, name); err != nil {
				return spawn.SwitchResult{}, fmt.Errorf("switch: acquire metadata lock: %w", err)
			}
			defer func() { _ = lock.ReleaseExclusiveNamed(stateDir, name) }()
		}
		meta, err := state.ReadTaskMeta(stateDir, request.ID)
		if err != nil {
			return spawn.SwitchResult{}, err
		}
		meta.SpawnGen = fmt.Sprintf("s%d", time.Now().UnixNano())
		if err := state.WriteTaskMeta(stateDir, meta); err != nil {
			return spawn.SwitchResult{}, err
		}
		at := time.Now().UTC()
		err = monitor.WriteObservation(stateDir, monitor.Observation{TaskID: meta.ID, Endpoint: (herdr.Target{}).String(), EndpointVerdict: monitor.ProbePresent, Digest: "resumed-work", LastObserved: at, LastSeen: at, LastProgress: at, Health: monitor.HealthBusy, Reason: monitor.None})
		return spawn.SwitchResult{}, err
	}
}

func TestBoardResumeRecoversDirectPRAndReportsPolicyDrift(t *testing.T) {
	// Arrange: the saved validation from an earlier direct-PR branch, and a
	// managed task whose frozen chain differs from the machine's. Only the
	// external terminal launch and gate tools are substituted.
	policy := versionSixPolicy(t, pipeline.Reviewer{})
	h, root := gateHome(t, policy, operatorMachineConfig)
	h.Data = filepath.Join(h.Root, "data")
	t.Setenv("CFO_HOME", h.Root)
	t.Setenv("CFO_STATE_OVERRIDE", h.State)
	t.Setenv("CFO_PROJECTS_ROOT", t.TempDir())
	t.Setenv("NM_HOME", root)
	if err := os.MkdirAll(h.Data, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"AGENTS.md", home.InstalledMarker} {
		if err := os.WriteFile(filepath.Join(h.Root, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	paused := map[string]state.Lifecycle{}
	for _, id := range []string{"direct-task", "managed-task"} {
		gatedTask(t, h, id, policy, claudeGoblin)
		meta, err := state.ReadTaskMeta(h.State, id)
		if err != nil {
			t.Fatal(err)
		}
		meta.Backend, meta.SpawnGen = "native", "generation-1"
		if id == "direct-task" {
			meta.Mode, meta.PipelineHash, meta.PipelineClass = "direct-PR", "", ""
		} else {
			older := legacyPipelineSelection(t, "ordinary")
			if err := older.Save(filepath.Join(meta.TaskTmp, "pipeline.json")); err != nil {
				t.Fatal(err)
			}
			meta.PipelineHash, meta.PipelineClass = older.Hash, older.Class
		}
		// The fixture names its tasks, so the supervisor's own naming pass has
		// nothing to write here: this test is the board's resume contract, not
		// a naming race, and a pre-named record keeps the two apart.
		meta.GoblinName, meta.GoblinTitle = "Bea", "Board Fixture"
		if err := state.WriteTaskMeta(h.State, meta); err != nil {
			t.Fatal(err)
		}
		at := time.Now().UTC().Add(-time.Hour)
		condition, err := state.NewPauseCondition("overlord", "", at)
		if err != nil {
			t.Fatal(err)
		}
		paused[id] = state.Lifecycle{ID: id, Generation: meta.SpawnGen, RequestGeneration: meta.SpawnGen, Operation: "pause-1", Action: "pause", Phase: "paused", Started: at, Updated: at, Pause: &condition, Reason: "overlord", GateRun: "run-1", GateIntent: "ship safely", GateHead: strings.Repeat("a", 40), NoticeSent: true}
		if err := state.WriteLifecycle(h.State, paused[id]); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(meta.Worktree, "unfinished.txt"), []byte("retained work"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runtime := commandRuntime{goblins: true, resolveHome: func() (home.Home, error) { return h, nil }}
	runtime.switchTask = boardResumeSwitch(h.State, true)
	runtime.taskLifecycle = func(ctx context.Context, _ home.Home, request lifecycle.Request, _ string) (state.Lifecycle, error) {
		controller := lifecycle.Service{StateDir: h.State, Operations: lifecycle.Operations{
			Memory: func() (uint64, uint64, error) { return 8 << 30, 8 << 30, nil },
			Notify: func(record state.Lifecycle) error { return lifecycle.Report(h.State, record) },
			Resume: func(ctx context.Context, meta state.TaskMeta, prior state.Lifecycle) error {
				var commands execx.Runner = absentResumeGate{}
				if meta.Mode == "no-mistakes" {
					commands = &resumedGateRunner{}
				}
				return resumeTask(ctx, h, runtime, commands, pipeline.Reader{Root: root, Commands: commands}, meta, prior, nil)
			},
		}}
		return controller.Run(ctx, request)
	}
	dispatch := &supervisor.Dispatch{
		Memory: func() (supervisor.Memory, error) {
			return supervisor.Memory{Available: 8 << 30, CommitAvailable: 8 << 30}, nil
		},
		Disk: func() (supervisor.Disk, error) { return supervisor.Disk{}, nil },
		Spawn: func(_ context.Context, args []string) (string, error) {
			var output, failure bytes.Buffer
			if code := runWithRuntime(args, &output, &failure, runtime); code != 0 {
				return failure.String(), fmt.Errorf("resume exited %d", code)
			}
			return output.String(), nil
		},
	}
	board, err := supervisor.Start(t.Context(), h, supervisor.Options{Dispatch: dispatch})
	if err != nil {
		t.Fatal(err)
	}
	defer board.Close()
	assets := t.TempDir()
	server := httptest.NewUnstartedServer(nil)
	handler := supervisor.NewHTTP(board, server.Listener.Addr().String(), os.DirFS(assets))
	server.Config.Handler = handler
	server.Start()
	defer server.Close()
	streamContext, cancelStream := context.WithTimeout(t.Context(), time.Minute)
	defer cancelStream()
	streamRequest, err := http.NewRequestWithContext(streamContext, "GET", server.URL+"/api/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := server.Client().Do(streamRequest)
	if err != nil {
		t.Fatalf("board event stream: %v", err)
	}
	defer stream.Body.Close()
	reader := bufio.NewReader(stream.Body)
	var event strings.Builder
	for !strings.HasSuffix(event.String(), "\n\n") {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("board event stream ended before its snapshot: %v", err)
		}
		event.WriteString(line)
	}
	if !strings.Contains(event.String(), "event: snapshot") || !strings.Contains(event.String(), "direct-task") || !strings.Contains(event.String(), "managed-task") {
		t.Fatalf("board event stream lost its tasks: %s", event.String())
	}
	stream.Body.Close()
	cancelStream()

	// Act: the browser variant clicks the shipped board against this same
	// backend. Ordinary CI exercises the identical request through the CLI.
	if os.Getenv("CFO_RESUME_BROWSER") == "1" {
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
		defer cancel()
		result, err := (execx.OSRunner{}).Run(ctx, execx.Request{
			Name:     "node",
			Args:     []string{"tests/resume-recovery-browser.mjs", server.URL, assets},
			Dir:      filepath.Join("..", "..", "frontend"),
			KillTree: true,
		})
		if err != nil || result.ExitCode != 0 {
			t.Fatalf("browser: exit %d, %v\n%s\n%s", result.ExitCode, err, result.Stdout, result.Stderr)
		}
		t.Log(string(result.Stdout))
	} else {
		for _, id := range []string{"direct-task", "managed-task"} {
			body, err := json.Marshal(map[string]string{"task": id, "generation": "generation-1", "operation": "resume-1", "action": "resume"})
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest("POST", server.URL+"/api/tasks/lifecycle", bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Origin", server.URL)
			request.Header.Set("X-CFO-Token", board.Instance)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != 202 {
				t.Fatalf("resume = %d %s", response.Code, response.Body)
			}
			if id == "direct-task" {
				awaitResumeBoard(t, board, h.State, id, "working")
			} else {
				awaitResumeBoard(t, board, h.State, id, "paused")
			}
		}
	}

	// Assert
	snapshot, err := board.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range snapshot.Tasks {
		meta, err := state.ReadTaskMeta(h.State, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(meta.Worktree, "unfinished.txt"))
		if err != nil || string(data) != "retained work" {
			t.Fatalf("unfinished work changed: %q %v", data, err)
		}
		record, err := state.ReadLifecycle(h.State, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if task.ID == "direct-task" {
			if task.Phase != "working" || task.ActionError != "" || record.Phase != "running" || meta.SpawnGen == "generation-1" {
				t.Fatalf("direct-PR task did not resume: %+v %+v", task, record)
			}
		} else if task.Phase != "paused" || !strings.Contains(task.ActionError, "cfo pipeline migrate managed-task") || !reflect.DeepEqual(record, paused[task.ID]) || meta.SpawnGen != "generation-1" {
			t.Fatalf("managed refusal lost its recovery instruction or pause: %+v %+v", task, record)
		}
	}
	records, err := wake.Pending(h.State)
	if err != nil {
		t.Fatal(err)
	}
	isReported := false
	for _, record := range records {
		if record.Key == "managed-task" && strings.Contains(record.Detail, "resume failed:") && strings.Contains(record.Detail, "cfo pipeline migrate managed-task") {
			isReported = true
		}
	}
	if !isReported {
		t.Fatalf("CFO did not receive the actual refusal: %+v", records)
	}
}

// awaitResumeBoard waits for the resumed task to settle on the outcome it is
// meant to have: the record's own operation completed with a new generation, or
// the managed refusal reported. Waiting only for working would hide a record
// still naming the paused generation (#1459).
func awaitResumeBoard(t *testing.T, board *supervisor.Service, stateDir, id, wantPhase string) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for time.Now().Before(deadline) {
		snapshot, err := board.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		for _, task := range snapshot.Tasks {
			if task.ID != id {
				continue
			}
			if wantPhase == "paused" && task.Phase == "paused" && strings.Contains(task.ActionError, "cfo pipeline migrate "+id) {
				return
			}
			if wantPhase != "working" || task.Phase != "working" {
				continue
			}
			record, err := state.ReadLifecycle(stateDir, id)
			if err != nil {
				continue
			}
			meta, err := state.ReadTaskMeta(stateDir, id)
			if err != nil {
				continue
			}
			// The record's own generation must be the one the metadata now
			// carries: a running record of the paused generation is exactly the
			// interleaving this waits out.
			if record.Phase == "running" && record.Operation == "resume-1" && meta.SpawnGen != "generation-1" && record.Generation == meta.SpawnGen {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("board resume did not settle on its own outcome")
}

// The fixture's switch takes the record's metadata lock exactly as production
// does, so a naming pass that read the record first cannot write the paused
// generation back over the resumed one (#1459). The naming pass here is the
// only concurrency in this file: the board-contract test above uses a
// pre-named fixture, so this narrow proof carries the race alone.
//
// Two orderings are proven. While a naming pass holds the record, the switch
// refuses with the lock error instead of writing into what that pass will put
// back. Once the record is free, the switch writes its own generation and the
// name the pass stored is kept.
func TestTheBoardSwitchRefusesWhileANamingPassHoldsTheRecord(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()
	meta := state.TaskMeta{ID: "task", Worktree: t.TempDir(), Backend: "native", SpawnGen: "generation-1"}
	if err := state.WriteTaskMeta(stateDir, meta); err != nil {
		t.Fatal(err)
	}
	path := state.TaskMetaPath(stateDir, "task")
	name := state.MetadataLockName("task")
	if _, err := lock.AcquireExclusiveNamed(stateDir, name); err != nil {
		t.Fatal(err)
	}
	released := false
	defer func() {
		if !released {
			_ = lock.ReleaseExclusiveNamed(stateDir, name)
		}
	}()
	held, err := state.ReadMeta(path)
	if err != nil {
		t.Fatal(err)
	}

	// Act: the switch runs against a held record.
	_, switchErr := boardResumeSwitch(stateDir, true)(context.Background(), home.Home{State: stateDir}, spawn.SwitchRequest{ID: "task"})

	// Assert: it refuses, and writes nothing.
	if switchErr == nil || !strings.Contains(switchErr.Error(), "acquire metadata lock") {
		t.Fatalf("switch = %v, want the lock refusal", switchErr)
	}
	unchanged, err := state.ReadMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged["spawn_gen"] != "generation-1" {
		t.Fatalf("a refused switch changed the record: %q", unchanged["spawn_gen"])
	}

	// Act: the naming pass finishes and the resume runs again.
	held["goblin_name"] = "Bea"
	if err := state.WriteMeta(path, held); err != nil {
		t.Fatal(err)
	}
	_ = lock.ReleaseExclusiveNamed(stateDir, name)
	released = true
	if _, err := boardResumeSwitch(stateDir, true)(context.Background(), home.Home{State: stateDir}, spawn.SwitchRequest{ID: "task"}); err != nil {
		t.Fatal(err)
	}

	// Assert: the resumed generation stands and the name survives it.
	final, err := state.ReadMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	if final["spawn_gen"] == "generation-1" || final["goblin_name"] != "Bea" {
		t.Fatalf("after the resume: gen %q name %q, want a new generation keeping the name", final["spawn_gen"], final["goblin_name"])
	}

	// The control: the same switch without the lock writes into the held
	// record, which is the overwrite the lock exists to refuse.
	if _, err := boardResumeSwitch(stateDir, false)(context.Background(), home.Home{State: stateDir}, spawn.SwitchRequest{ID: "task"}); err != nil {
		t.Fatal(err)
	}
	unlocked, err := state.ReadMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	if unlocked["spawn_gen"] == final["spawn_gen"] {
		t.Fatalf("the unlocked switch wrote nothing, so it does not show the overwrite it prevents")
	}
}
