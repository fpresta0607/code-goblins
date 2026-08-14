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

func TestResolverResolvesTaskSelectorsAndExplicitTargets(t *testing.T) {
	stateDir := t.TempDir()
	meta := taskMeta("task-7", "claude")
	if err := state.WriteTaskMeta(stateDir, meta); err != nil {
		t.Fatal(err)
	}
	fmBuild := taskMeta("fm-build", "codex")
	fmBuild.HerdrPaneID = "pane-fm-build"
	if err := state.WriteTaskMeta(stateDir, fmBuild); err != nil {
		t.Fatal(err)
	}

	resolver := Resolver{StateDir: stateDir}
	for _, test := range []struct {
		name     string
		raw      string
		want     herdr.Target
		wantMeta state.TaskMeta
	}{
		{
			name:     "task ID",
			raw:      "task-7",
			want:     herdr.Target{Session: "fleet", Pane: "pane-7"},
			wantMeta: meta,
		},
		{
			name:     "fm task ID",
			raw:      "fm-task-7",
			want:     herdr.Target{Session: "fleet", Pane: "pane-7"},
			wantMeta: meta,
		},
		{
			name:     "exact task ID takes priority over fm selector fallback",
			raw:      "fm-build",
			want:     herdr.Target{Session: "fleet", Pane: "pane-fm-build"},
			wantMeta: fmBuild,
		},
		{
			name: "explicit target retains pane colons",
			raw:  "custom:workspace:pane",
			want: herdr.Target{Session: "custom", Pane: "workspace:pane"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, gotMeta, err := resolver.Resolve(context.Background(), test.raw)
			if err != nil {
				t.Fatalf("Resolve(%q): %v", test.raw, err)
			}
			if got != test.want {
				t.Errorf("Resolve(%q) target = %#v, want %#v", test.raw, got, test.want)
			}
			if gotMeta != test.wantMeta {
				t.Errorf("Resolve(%q) metadata = %#v, want %#v", test.raw, gotMeta, test.wantMeta)
			}
		})
	}
}

func TestResolverRejectsUnknownAndBarePaneSelectors(t *testing.T) {
	resolver := Resolver{StateDir: t.TempDir()}

	for _, test := range []struct {
		name string
		raw  string
		want string
	}{
		{name: "unknown task", raw: "missing-task", want: "unknown selector"},
		{name: "bare pane", raw: "pane-7", want: "<session>:<pane-id>"},
		{name: "malformed target", raw: "fleet:", want: "<session>:<pane-id>"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := resolver.Resolve(context.Background(), test.raw)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Resolve(%q) error = %v, want text %q", test.raw, err, test.want)
			}
		})
	}
}

func TestResolverRejectsNonHerdrMetadata(t *testing.T) {
	stateDir := t.TempDir()
	meta := taskMeta("task-7", "claude")
	meta.Backend = "tmux"
	if err := state.WriteTaskMeta(stateDir, meta); err != nil {
		t.Fatal(err)
	}

	_, _, err := (Resolver{StateDir: stateDir}).Resolve(context.Background(), "task-7")
	if err == nil || !strings.Contains(err.Error(), "Herdr") {
		t.Fatalf("Resolve non-Herdr metadata error = %v, want Herdr refusal", err)
	}
}

func taskMeta(id, harness string) state.TaskMeta {
	return state.TaskMeta{
		ID:               id,
		Harness:          harness,
		Kind:             "ship",
		Backend:          "herdr",
		HerdrSession:     "fleet",
		HerdrWorkspaceID: "workspace-7",
		HerdrTabID:       "tab-7",
		HerdrPaneID:      "pane-7",
	}
}

type fakeResolver struct {
	target herdr.Target
	meta   state.TaskMeta
	err    error
	raws   []string
}

func (r *fakeResolver) Resolve(_ context.Context, raw string) (herdr.Target, state.TaskMeta, error) {
	r.raws = append(r.raws, raw)
	return r.target, r.meta, r.err
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

func rawReply(text string) runnerReply {
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

func assertErrorContains(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want text %q", err, want)
	}
}
