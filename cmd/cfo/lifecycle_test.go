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
		// A stop's record keeps the pause before it, which says nothing of
		// the stop.
		{"a stop after a pause", state.Lifecycle{Operation: "op-2", Action: "stop", Reason: "Requested from the board", Pause: &state.PauseCondition{Reason: "overlord"}},
			[]lifecycle.Request{{ID: "g1-h1", Generation: "s2", Action: "stop", Reason: "Stopped with its parent g1: Requested from the board"}}, []string{"helper g1-h1 stopped"}},
		{"a stop after a wait on that helper", state.Lifecycle{Operation: "op-2", Action: "stop", Reason: "Requested from the board", Pause: &state.PauseCondition{Reason: "dependency", Until: "task:g1-h1"}},
			[]lifecycle.Request{{ID: "g1-h1", Generation: "s2", Action: "stop", Reason: "Stopped with its parent g1: Requested from the board"}}, []string{"helper g1-h1 stopped"}},
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

// cfo pause takes what resumes the goblin as a condition, the same conditions
// the supervisor's scheduler reads, so a goblin paused until a time, a task or
// a pull request resumes by itself: a goblin paused for Codex's weekly reset
// resumes at the reset. A pause naming nothing that resumes it is refused, as
// one naming two conditions is; on 2026-10-07 the live fleet's pauses all read
// "Requested by the operator" and none could resume.
func TestPauseTakesTheConditionThatResumesIt(t *testing.T) {
	const pullRequest = "https://github.com/fpresta0607/code-goblins/pull/9"
	cases := []struct {
		name                string
		args                []string
		wantReason, wantFor string
		wantRefusal         string
	}{
		{name: "until a time", args: []string{"--until", "2026-10-09T13:39:00Z"}, wantReason: "dependency", wantFor: "date:2026-10-09T13:39:00Z"},
		{name: "until a task delivers", args: []string{"--until-task", "other-task"}, wantReason: "dependency", wantFor: "task:other-task"},
		{name: "until a pull request merges", args: []string{"--until-pr", pullRequest}, wantReason: "dependency", wantFor: "pr:" + pullRequest},
		{name: "a dependency named in full", args: []string{"--until", "task:other-task"}, wantReason: "dependency", wantFor: "task:other-task"},
		{name: "an allowance reset", args: []string{"--reason", "allowance", "--until", "2026-10-09T13:39:00Z"}, wantReason: "allowance", wantFor: "2026-10-09T13:39:00Z"},
		{name: "nothing resumes it", args: []string{}, wantRefusal: "--until-task"},
		{name: "two conditions", args: []string{"--until-task", "other-task", "--until-pr", pullRequest}, wantRefusal: "one condition"},
		{name: "an invalid task", args: []string{"--until-task", "Not A Task"}, wantRefusal: "task"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			h := primaryHomeFixture(t)
			if err := state.WriteTaskMeta(h.State, state.TaskMeta{ID: "task", SpawnGen: "session-1"}); err != nil {
				t.Fatal(err)
			}
			var received *lifecycle.Request
			runtime := commandRuntime{resolveHome: func() (home.Home, error) { return h, nil }, taskLifecycle: func(_ context.Context, _ home.Home, request lifecycle.Request, _ string) (state.Lifecycle, error) {
				received = &request
				return state.Lifecycle{ID: request.ID, Phase: "paused"}, nil
			}}
			var output, failure bytes.Buffer

			// Act
			code := runWithRuntime(append([]string{"pause", "task"}, testCase.args...), &output, &failure, runtime)

			// Assert
			if testCase.wantRefusal != "" {
				if code != 2 || received != nil || !strings.Contains(failure.String(), testCase.wantRefusal) {
					t.Fatalf("code=%d request=%+v error=%q, want refused naming %q", code, received, failure.String(), testCase.wantRefusal)
				}
				return
			}
			if code != 0 || received == nil || received.Reason != testCase.wantReason || received.Until != testCase.wantFor {
				t.Fatalf("code=%d request=%+v error=%q, want reason %q until %q", code, received, failure.String(), testCase.wantReason, testCase.wantFor)
			}
		})
	}
}
