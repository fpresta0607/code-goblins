package fleet

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// A message or a peek reaches a goblin only through its native terminal, so a
// target names a task only when its record says it runs in one.
func TestNativeTaskNamesOnlyATaskRecordedInANativeTerminal(t *testing.T) {
	stateDir := t.TempDir()
	for _, meta := range []state.TaskMeta{
		{ID: "task-7", Harness: "claude", Kind: "ship", Backend: "native"},
		{ID: "task-8", Harness: "claude", Kind: "ship", Backend: "herdr", HerdrSession: "fleet", HerdrWorkspaceID: "workspace-8", HerdrTabID: "tab-8", HerdrPaneID: "pane-8"},
	} {
		if err := state.WriteTaskMeta(stateDir, meta); err != nil {
			t.Fatal(err)
		}
	}

	for _, test := range []struct {
		target string
		wantID string
		native bool
	}{
		{target: "task-7", wantID: "task-7", native: true},
		{target: "gb-task-7", wantID: "task-7", native: true},
		{target: "task-8", wantID: "task-8", native: false},
		{target: "task-9", wantID: "", native: false},
		{target: "fleet:pane-7", wantID: "", native: false},
	} {
		t.Run(test.target, func(t *testing.T) {
			meta, native := NativeTask(stateDir, test.target)
			if meta.ID != test.wantID || native != test.native {
				t.Errorf("NativeTask(%q) = %q, %v; want %q, %v", test.target, meta.ID, native, test.wantID, test.native)
			}
		})
	}
}

type runnerReply struct {
	result execx.Result
	err    error
}

type fakeRunner struct {
	replies  []runnerReply
	requests []execx.Request
}

func (r *fakeRunner) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	r.requests = append(r.requests, request)
	if len(r.replies) == 0 {
		return execx.Result{}, fmt.Errorf("unexpected request: %s %s", request.Name, strings.Join(request.Args, " "))
	}
	reply := r.replies[0]
	r.replies = r.replies[1:]
	return reply.result, reply.err
}

func jsonReply(text string) runnerReply {
	return runnerReply{result: execx.Result{Stdout: []byte(text)}}
}

func newHerdrClient(runner *fakeRunner, sleeps *[]time.Duration) *herdr.Client {
	return &herdr.Client{
		Commands: runner,
		Sleep: func(_ context.Context, duration time.Duration) error {
			*sleeps = append(*sleeps, duration)
			return nil
		},
	}
}

func assertRequests(t *testing.T, got []execx.Request, want [][]string) {
	t.Helper()
	gotArgs := make([][]string, len(got))
	for index, request := range got {
		if request.Name != "herdr" {
			t.Fatalf("request %d name = %q, want herdr", index, request.Name)
		}
		gotArgs[index] = request.Args
	}
	if !reflect.DeepEqual(gotArgs, want) {
		t.Errorf("Herdr requests = %#v, want %#v", gotArgs, want)
	}
}
