package supervisor

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestRecoverTasksSkipsLiveTasksPreservesSettingsAndContinuesAfterFailure(t *testing.T) {
	tasks := []state.TaskMeta{{ID: "live"}, {ID: "failed", Harness: "codex", Model: "model", Effort: "high", Worktree: "existing"}, {ID: "resumed", Harness: "pi"}}
	var resumed []string
	recovery := TaskRecovery{
		Tasks:     func() ([]state.TaskMeta, error) { return tasks, nil },
		IsRunning: func(_ context.Context, task state.TaskMeta) (bool, error) { return task.ID == "live", nil },
		Memory:    func() (uint64, uint64, error) { return 8 << 30, 32 << 30, nil },
		Resume: func(_ context.Context, task state.TaskMeta, session string) error {
			resumed = append(resumed, task.ID)
			if task.ID == "failed" {
				if task.Model != "model" || task.Effort != "high" || task.Worktree != "existing" {
					t.Errorf("changed settings: %+v", task)
				}
				return errors.New("sign-in needed")
			}
			return nil
		},
	}
	database := Database{TaskSessions: map[string]string{"failed": "codex/session", "resumed": "pi/session"}, Sessions: map[string]Session{
		"codex/session": {NativeID: "abc01234-5678-9012-abcd-012345678901", Harness: "codex", TaskID: "failed"},
		"pi/session":    {NativeID: "abc01234-5678-9012-abcd-012345678902", Harness: "pi", TaskID: "resumed"},
	}}
	results, err := recovery.Run(context.Background(), database)
	if err != nil || len(results) != 3 || len(resumed) != 2 {
		t.Fatalf("results=%+v resumed=%q err=%v", results, resumed, err)
	}
	for index, want := range []string{"Already running", "Needs a hand", "Resumed"} {
		if results[index].Status != want {
			t.Errorf("result=%+v want=%s", results[index], want)
		}
	}
}

func TestTaskRecoveryEndpointValidatesRequestsAndReportsFailure(t *testing.T) {
	for _, test := range []struct {
		name, origin, token, body string
		isFailed                  bool
		want                      int
	}{
		{"success", "http://board.local", "instance", `{}`, false, 200},
		{"foreign origin", "http://evil.local", "instance", `{}`, false, 403},
		{"missing token", "http://board.local", "", `{}`, false, 403},
		{"unknown input", "http://board.local", "instance", `{"force":true}`, false, 400},
		{"failure", "http://board.local", "instance", `{}`, true, 409},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, _ := testStore(t)
			wasCalled := false
			recovery := &TaskRecovery{Tasks: func() ([]state.TaskMeta, error) {
				wasCalled = true
				if test.isFailed {
					return nil, errors.New("task records unavailable")
				}
				return nil, nil
			}}
			service := &Service{Store: store, Instance: "instance", Options: Options{TaskRecovery: recovery}, subscribers: map[chan struct{}]struct{}{}}
			request := httptest.NewRequest("POST", "http://board.local/api/tasks/resume", strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Origin", test.origin)
			request.Header.Set("X-CFO-Token", test.token)
			response := httptest.NewRecorder()
			NewHTTP(service, "board.local", nil).ServeHTTP(response, request)
			if response.Code != test.want || wasCalled != (test.want == 200 || test.want == 409) {
				t.Fatalf("status=%d body=%s called=%t", response.Code, response.Body.String(), wasCalled)
			}
		})
	}
}

func TestRecoverTasksReportsMissingSessionAndRechecksMemoryBeforeEachStart(t *testing.T) {
	checks := 0
	recovery := TaskRecovery{
		Tasks: func() ([]state.TaskMeta, error) {
			return []state.TaskMeta{{ID: "missing", Harness: "claude"}, {ID: "one", Harness: "claude"}, {ID: "two", Harness: "claude"}}, nil
		},
		IsRunning: func(context.Context, state.TaskMeta) (bool, error) { return false, nil },
		Memory: func() (uint64, uint64, error) {
			checks++
			if checks > 1 {
				return 4 << 30, 32 << 30, nil
			}
			return 8 << 30, 32 << 30, nil
		},
		Resume: func(_ context.Context, task state.TaskMeta, _ string) error {
			if task.ID != "one" {
				t.Fatal("started without memory/session")
			}
			return nil
		},
	}
	database := Database{TaskSessions: map[string]string{"one": "one", "two": "two"}, Sessions: map[string]Session{}}
	for _, id := range []string{"one", "two"} {
		database.Sessions[id] = Session{TaskID: id, Harness: "claude", NativeID: "abc01234-5678-9012-abcd-012345678901"}
	}
	results, err := recovery.Run(context.Background(), database)
	if err != nil || len(results) != 3 || results[0].Status != "Needs a hand" || results[1].Status != "Resumed" || results[2].Status != "Waiting for memory" {
		t.Fatalf("results=%+v err=%v", results, err)
	}
}

func TestRecoveryCanRetryBeforeTheNewGenerationHasReportedItsSession(t *testing.T) {
	const session = "abc01234-5678-9012-abcd-012345678901"
	task := state.TaskMeta{ID: "retry", Harness: "codex", SpawnGen: "replacement", ResumeSession: session}
	wasResumed := false
	recovery := TaskRecovery{
		Tasks:     func() ([]state.TaskMeta, error) { return []state.TaskMeta{task}, nil },
		IsRunning: func(context.Context, state.TaskMeta) (bool, error) { return false, nil },
		Memory:    func() (uint64, uint64, error) { return 8 << 30, 32 << 30, nil },
		Resume: func(_ context.Context, _ state.TaskMeta, id string) error {
			wasResumed = true
			if id != session {
				t.Fatalf("resumed %q", id)
			}
			return nil
		},
	}
	results, err := recovery.Run(context.Background(), Database{})
	if err != nil || !wasResumed || len(results) != 1 || results[0].Status != "Resumed" {
		t.Fatalf("results=%+v resumed=%t error=%v", results, wasResumed, err)
	}
}
