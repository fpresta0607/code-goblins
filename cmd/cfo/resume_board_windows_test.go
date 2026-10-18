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
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

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
	runtime.switchTask = func(_ context.Context, _ home.Home, request spawn.SwitchRequest) (spawn.SwitchResult, error) {
		meta, err := state.ReadTaskMeta(h.State, request.ID)
		if err != nil {
			return spawn.SwitchResult{}, err
		}
		meta.SpawnGen = fmt.Sprintf("s%d", time.Now().UnixNano())
		if err := state.WriteTaskMeta(h.State, meta); err != nil {
			return spawn.SwitchResult{}, err
		}
		at := time.Now().UTC()
		err = monitor.WriteObservation(h.State, monitor.Observation{TaskID: meta.ID, Endpoint: (herdr.Target{}).String(), EndpointVerdict: monitor.ProbePresent, Digest: "resumed-work", LastObserved: at, LastSeen: at, LastProgress: at, Health: monitor.HealthBusy, Reason: monitor.None})
		return spawn.SwitchResult{}, err
	}
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
			awaitResumeBoard(t, board, id)
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

func awaitResumeBoard(t *testing.T, board *supervisor.Service, id string) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for time.Now().Before(deadline) {
		snapshot, err := board.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		for _, task := range snapshot.Tasks {
			if task.ID == id && (task.Phase == "working" || task.ActionError != "") {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("board resume did not settle")
}
