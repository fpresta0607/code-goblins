package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestNotifyPreparedWaitRequiresExclusiveStructuredArguments(t *testing.T) {
	for _, args := range [][]string{
		{"--prepared-wait", "task:other"},
		{"--generation", "current", "--handoff", "handoff.md"},
		{"--prepared-wait", "task:other", "--generation", "current", "--handoff", "handoff.md", "--working", "still working"},
		{"--prepared-wait", "task:other", "--generation", "current", "--handoff", "handoff.md", "--waiting-on", "other", "why"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := runNotify(append([]string{"fixture"}, args...), &stdout, &stderr, defaultCommandRuntime()); code != 2 || !strings.Contains(stderr.String(), "--prepared-wait requires") {
				t.Fatalf("code=%d, stderr=%s", code, stderr.String())
			}
		})
	}
}

func TestNotifyPreparedWaitDoesNotTrustTaskEnvironment(t *testing.T) {
	directory := t.TempDir()
	stateDirectory := filepath.Join(directory, "state")
	t.Setenv("CEO_HOME", directory)
	t.Setenv("CEO_STATE_OVERRIDE", stateDirectory)
	t.Setenv("CEO_TASK_ID", "fixture")
	t.Setenv("CEO_SPAWN_GEN", "current")
	if err := os.MkdirAll(stateDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteTaskMeta(stateDirectory, state.TaskMeta{ID: "fixture", Backend: "native", SpawnGen: "current"}); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := runNotify([]string{"fixture", "--prepared-wait", "task:other", "--generation", "current", "--handoff", "handoff.md"}, &stdout, &stderr, defaultCommandRuntime())
	if code != 1 || !strings.Contains(stderr.String(), "no host record") {
		t.Fatalf("code=%d, stderr=%s", code, stderr.String())
	}
	if _, err := state.ReadPreparedWait(stateDirectory, "fixture"); !os.IsNotExist(err) {
		t.Fatalf("unauthenticated signal was recorded: %v", err)
	}
}

func TestNotifyOrdinaryWaitDoesNotAuthorizeLifecycle(t *testing.T) {
	directory := t.TempDir()
	stateDirectory := filepath.Join(directory, "state")
	t.Setenv("CEO_HOME", directory)
	t.Setenv("CEO_STATE_OVERRIDE", stateDirectory)
	if err := os.MkdirAll(stateDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := runNotify([]string{"fixture", "--waiting-on", "other", "package prepared"}, &stdout, &stderr, defaultCommandRuntime()); code != 0 {
		t.Fatalf("code=%d, stderr=%s", code, stderr.String())
	}
	if _, err := state.ReadPreparedWait(stateDirectory, "fixture"); !os.IsNotExist(err) {
		t.Fatalf("prose authorized lifecycle: %v", err)
	}
}
