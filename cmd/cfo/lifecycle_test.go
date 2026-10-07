package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"slices"
	"strings"
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

// Pausing or stopping a parent runs the same operation on each live helper,
// once per parent operation whatever retries it, a pause keeping the
// parent's condition; a parent paused until its own helper finishes leaves
// that helper at work, and a helper that refuses is named, never hidden.
func TestReachHelpersRunsTheParentsActionOnEachHelper(t *testing.T) {
	cases := []struct {
		name   string
		record state.Lifecycle
		want   []lifecycle.Request
		lines  []string
	}{
		{"a pause", state.Lifecycle{Operation: "op-1", Action: "pause", Pause: &state.PauseCondition{Reason: "memory"}},
			[]lifecycle.Request{{ID: "g1-h1", Generation: "s2", Action: "pause", Reason: "memory"}}, []string{"helper g1-h1 paused"}},
		{"a stop", state.Lifecycle{Operation: "op-1", Action: "stop", Reason: "Requested from the board"},
			[]lifecycle.Request{{ID: "g1-h1", Generation: "s2", Action: "stop", Reason: "Stopped with its parent g1: Requested from the board"}}, []string{"helper g1-h1 stopped"}},
		{"a wait on that helper", state.Lifecycle{Operation: "op-1", Action: "pause", Pause: &state.PauseCondition{Reason: "dependency", Until: "task:g1-h1"}},
			nil, []string{"helper g1-h1 kept at work: g1 waits on it"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			h := testHome(t)
			for _, meta := range []state.TaskMeta{{ID: "g1", SpawnGen: "s1"}, {ID: "g1-h1", SpawnGen: "s2", Parent: "g1"}, {ID: "g2-h1", SpawnGen: "s3", Parent: "g2"}} {
				meta.Window, meta.Harness, meta.Kind, meta.Backend, meta.Worktree = "native", "claude", "ship", "native", h.Root+`\`+meta.ID
				if err := os.MkdirAll(h.State, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := state.WriteTaskMeta(h.State, meta); err != nil {
					t.Fatal(err)
				}
			}
			var ran []lifecycle.Request
			run := func(_ context.Context, _ home.Home, request lifecycle.Request, _ string) (state.Lifecycle, error) {
				ran = append(ran, request)
				return state.Lifecycle{Phase: map[string]string{"pause": "paused", "stop": "stopped"}[request.Action]}, nil
			}

			// Act
			lines, err := reachHelpers(context.Background(), h, state.TaskMeta{ID: "g1"}, &c.record, run)
			again, _ := reachHelpers(context.Background(), h, state.TaskMeta{ID: "g1"}, &c.record, run)

			// Assert
			if err != nil || !slices.Equal(lines, c.lines) || !slices.Equal(again, c.lines) {
				t.Errorf("lines = %q, %v; want %q", lines, err, c.lines)
			}
			if len(ran) != 2*len(c.want) {
				t.Fatalf("ran %+v, want %+v twice", ran, c.want)
			}
			for i, want := range c.want {
				got := ran[i]
				if got.Operation == "" || got.Operation != ran[i+len(c.want)].Operation || state.ValidTaskID(got.Operation) != nil {
					t.Errorf("operation %q, then %q: want one valid operation for every retry", got.Operation, ran[i+len(c.want)].Operation)
				}
				got.Operation = ""
				if got != want {
					t.Errorf("ran %+v, want %+v", got, want)
				}
			}
		})
	}
}

func TestReachHelpersNamesAHelperThatRefused(t *testing.T) {
	h := testHome(t)
	if err := os.MkdirAll(h.State, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteTaskMeta(h.State, state.TaskMeta{ID: "g1-h1", SpawnGen: "s2", Parent: "g1", Window: "native", Harness: "claude", Kind: "ship", Backend: "native"}); err != nil {
		t.Fatal(err)
	}
	run := func(context.Context, home.Home, lifecycle.Request, string) (state.Lifecycle, error) {
		return state.Lifecycle{}, errors.New("its terminal did not answer")
	}

	_, err := reachHelpers(context.Background(), h, state.TaskMeta{ID: "g1"}, &state.Lifecycle{Operation: "op-1", Action: "stop"}, run)

	if err == nil || !strings.Contains(err.Error(), "helper g1-h1: its terminal did not answer") {
		t.Errorf("reachHelpers = %v, want the helper's refusal named", err)
	}
}
