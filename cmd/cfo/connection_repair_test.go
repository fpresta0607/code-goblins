package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestConnectionRepairCLIRefusesMissingAndReplacedTasksBeforeLogin(t *testing.T) {
	directory := t.TempDir()
	if err := os.MkdirAll(filepath.Join(directory, "state"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFO_HOME", directory)
	t.Setenv("CFO_STATE_OVERRIDE", filepath.Join(directory, "state"))
	if err := state.WriteTaskMeta(filepath.Join(directory, "state"), state.TaskMeta{ID: "sample", SpawnGen: "current", Project: directory, Worktree: directory, Backend: "native", Harness: "claude"}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		args    []string
		code    int
		message string
	}{
		{nil, 2, "usage:"},
		{[]string{"missing", "current", "service:repo", "cli"}, 1, "Task changed"},
		{[]string{"sample", "old", "service:repo", "cli"}, 1, "Task changed"},
		{[]string{"sample", "current", "service:repo", "unoffered"}, 1, "Refresh this connection"},
	} {
		var stdout, stderr strings.Builder
		code := runConnectionRepair(test.args, &stdout, &stderr)
		if code != test.code || !strings.Contains(stderr.String(), test.message) || stdout.Len() != 0 {
			t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
	}
}
