package main

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lifecycle"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestLifecycleCommandsUseTheSameServiceAndSurfaceFailures(t *testing.T) {
	for _, command := range []string{"pause", "resume", "kill"} {
		t.Run(command, func(t *testing.T) {
			h := primaryHomeFixture(t)
			if err := state.WriteTaskMeta(h.State, state.TaskMeta{ID: "task", SpawnGen: "session-1"}); err != nil {
				t.Fatal(err)
			}
			var received lifecycle.Request
			runtime := commandRuntime{resolveHome: func() (home.Home, error) { return h, nil }, taskLifecycle: func(_ context.Context, _ home.Home, request lifecycle.Request, _ string) (state.Lifecycle, error) {
				received = request
				return state.Lifecycle{}, errors.New("fixture refusal")
			}}
			var output, failure bytes.Buffer
			code := runWithRuntime([]string{command, "task", "--operation", "request-1", "--reason", "overlord"}, &output, &failure, runtime)
			action := command
			if command == "kill" {
				action = "stop"
			}
			if code != 1 || received.Action != action || received.Generation != "session-1" || received.Operation != "request-1" || failure.String() != "fixture refusal\n" {
				t.Fatalf("command=%d request=%+v error=%s", code, received, &failure)
			}
		})
	}
}
