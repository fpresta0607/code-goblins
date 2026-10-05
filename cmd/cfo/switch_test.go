package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// cfo switch changes a goblin's harness, model or effort and needs one of
// them; it moves nothing out of Herdr any more, so --native is refused.
func TestRunSwitchNeedsAHarnessModelOrEffort(t *testing.T) {
	for name, test := range map[string]struct {
		args    []string
		exit    int
		refusal string
	}{
		"a model":    {[]string{"--model", "gpt-5"}, 0, ""},
		"no options": {nil, 2, "one of --harness, --model, or --effort is required"},
		"--native":   {[]string{"--native"}, 2, "-native"},
	} {
		t.Run(name, func(t *testing.T) {
			h := testHome(t)
			if err := os.MkdirAll(h.State, 0o755); err != nil {
				t.Fatal(err)
			}
			deps := testCommandRuntimeForHome(h)
			if err := state.WriteTaskMeta(h.State, state.TaskMeta{ID: "g4", Harness: "codex", SpawnGen: "g1"}); err != nil {
				t.Fatal(err)
			}
			var got *spawn.SwitchRequest
			deps.switchTask = func(_ context.Context, _ home.Home, request spawn.SwitchRequest) (spawn.SwitchResult, error) {
				got = &request
				return spawn.SwitchResult{Output: "switched g4"}, nil
			}
			deps.speedHint = func(context.Context, string) string { return "" }

			var stdout, stderr bytes.Buffer
			exit := runWithRuntime(append([]string{"switch", "g4"}, test.args...), &stdout, &stderr, deps)

			if exit != test.exit {
				t.Fatalf("exit = %d, want %d; stderr=%s", exit, test.exit, stderr.String())
			}
			switch {
			case test.exit != 0 && (got != nil || !strings.Contains(stderr.String(), test.refusal)):
				t.Errorf("request = %+v, stderr = %q; want refused saying %q", got, stderr.String(), test.refusal)
			case test.exit == 0 && (got == nil || got.ID != "g4" || got.Model != "gpt-5"):
				t.Errorf("request = %+v, want g4 switched to gpt-5", got)
			}
		})
	}
}

func TestRunSwitchSelectsOnlyTheTasksOwnGenerationSession(t *testing.T) {
	for _, testCase := range []struct {
		name                string
		link                string
		harness             string
		targetHarness       string
		isMissingGeneration bool
		wantSession         string
	}{
		{name: "owned codex", link: "owned", harness: "codex", wantSession: "owned-session"},
		{name: "owned claude", link: "owned", harness: "claude", wantSession: "owned-session"},
		{name: "foreign latest only", link: "missing", harness: "codex"},
		{name: "foreign task link", link: "foreign-same-generation", harness: "codex"},
		{name: "stale generation", link: "stale", harness: "codex"},
		{name: "different harness", link: "different-harness", harness: "codex"},
		{name: "CFO session", link: "cfo", harness: "codex"},
		{name: "missing generation", link: "owned", harness: "codex", isMissingGeneration: true},
		{name: "different target harness", link: "owned", harness: "codex", targetHarness: "claude"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			h := testHome(t)
			if err := os.MkdirAll(h.State, 0o755); err != nil {
				t.Fatal(err)
			}
			meta := state.TaskMeta{ID: "task-7", Harness: testCase.harness, Backend: "native", SpawnGen: "s1"}
			if testCase.isMissingGeneration {
				meta.SpawnGen = ""
			}
			other := state.TaskMeta{ID: "task-8", Harness: testCase.harness, Backend: "native", SpawnGen: "s2"}
			for _, task := range []state.TaskMeta{meta, other} {
				if err := state.WriteTaskMeta(h.State, task); err != nil {
					t.Fatal(err)
				}
			}
			database := supervisor.Database{
				TaskSessions: map[string]string{meta.ID: testCase.link, other.ID: "foreign"},
				Sessions: map[string]supervisor.Session{
					"owned":                   {NativeID: "owned-session", TaskID: meta.ID, Generation: meta.SpawnGen, Harness: meta.Harness, Role: "goblin", Phase: "ended"},
					"foreign":                 {NativeID: "foreign-latest-session", TaskID: other.ID, Generation: other.SpawnGen, Harness: other.Harness, Role: "goblin", Phase: "ended"},
					"foreign-same-generation": {NativeID: "foreign-latest-session", TaskID: other.ID, Generation: meta.SpawnGen, Harness: other.Harness, Role: "goblin", Phase: "ended"},
					"stale":                   {NativeID: "stale-session", TaskID: meta.ID, Generation: "previous-generation", Harness: meta.Harness, Role: "goblin"},
					"different-harness":       {NativeID: "other-harness-session", TaskID: meta.ID, Generation: meta.SpawnGen, Harness: "claude", Role: "goblin"},
					"cfo":                     {NativeID: "cfo-session", TaskID: meta.ID, Generation: meta.SpawnGen, Harness: meta.Harness, Role: "cfo"},
				},
			}
			data, err := json.Marshal(database)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(h.State, ".supervisor.json"), data, 0o600); err != nil {
				t.Fatal(err)
			}
			deps := testCommandRuntimeForHome(h)
			var received spawn.SwitchRequest
			deps.switchTask = func(_ context.Context, _ home.Home, request spawn.SwitchRequest) (spawn.SwitchResult, error) {
				received = request
				return spawn.SwitchResult{Output: "switched task-7"}, nil
			}
			deps.speedHint = func(context.Context, string) string { return "" }
			var stdout, stderr bytes.Buffer

			args := []string{"switch", meta.ID, "--effort", "xhigh"}
			if testCase.targetHarness != "" {
				args = append(args, "--harness", testCase.targetHarness)
			}
			exit := runWithRuntime(args, &stdout, &stderr, deps)

			if exit != 0 || received.ID != meta.ID || received.Generation != meta.SpawnGen || received.ResumeSession != testCase.wantSession || received.IsResume {
				t.Fatalf("exit=%d request=%+v stderr=%s, want only owned session %q of generation %s", exit, received, stderr.String(), testCase.wantSession, meta.SpawnGen)
			}
		})
	}
}

func TestRunSwitchRefusesUnreadableSessionOwnershipBeforeLaunching(t *testing.T) {
	h := testHome(t)
	if err := os.MkdirAll(h.State, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteTaskMeta(h.State, state.TaskMeta{ID: "task-7", Harness: "codex", SpawnGen: "s1"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.State, ".supervisor.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	deps := testCommandRuntimeForHome(h)
	isCalled := false
	deps.switchTask = func(context.Context, home.Home, spawn.SwitchRequest) (spawn.SwitchResult, error) {
		isCalled = true
		return spawn.SwitchResult{}, nil
	}
	deps.speedHint = func(context.Context, string) string { return "" }
	var stdout, stderr bytes.Buffer

	exit := runWithRuntime([]string{"switch", "task-7", "--effort", "xhigh"}, &stdout, &stderr, deps)

	if exit != 1 || isCalled || !strings.Contains(stderr.String(), "session ownership") {
		t.Fatalf("exit=%d called=%v stderr=%s, want refusal before switching", exit, isCalled, stderr.String())
	}
}

func TestRunSwitchBindsABoardRequestToTheSelectedGeneration(t *testing.T) {
	h := testHome(t)
	if err := os.MkdirAll(h.State, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteTaskMeta(h.State, state.TaskMeta{ID: "g4", Harness: "codex", SpawnGen: "s1"}); err != nil {
		t.Fatal(err)
	}
	deps := testCommandRuntimeForHome(h)
	var got spawn.SwitchRequest
	deps.switchTask = func(_ context.Context, _ home.Home, request spawn.SwitchRequest) (spawn.SwitchResult, error) {
		got = request
		return spawn.SwitchResult{Output: "switched g4"}, nil
	}
	deps.speedHint = func(context.Context, string) string { return "" }
	var stdout, stderr bytes.Buffer

	exit := runWithRuntime([]string{"switch", "g4", "--generation", "s1", "--harness", "codex", "--model", "gpt-6.1-sol", "--effort", "high", "--force-dirty"}, &stdout, &stderr, deps)

	if exit != 0 || got.Generation != "s1" || !got.ForceDirty {
		t.Fatalf("generation-bound switch = %d, %+v: %s", exit, got, stderr.String())
	}
}
