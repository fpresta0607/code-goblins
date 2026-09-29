package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/spawn"
)

// cfo switch --native moves a task into a native terminal with no other
// option, keeping its harness, model and effort; with no option at all there
// is nothing to switch, and a change of harness, model or effort is a separate
// switch after the move.
func TestRunSwitchMovesATaskIntoANativeTerminal(t *testing.T) {
	for name, test := range map[string]struct {
		args    []string
		exit    int
		native  bool
		refusal string
	}{
		"native":              {[]string{"--native"}, 0, true, ""},
		"no options":          {nil, 2, false, "--native"},
		"native with harness": {[]string{"--native", "--harness", "claude"}, 2, false, "--native keeps the task's harness, model and effort"},
		"native with model":   {[]string{"--native", "--model", "gpt-5"}, 2, false, "--native keeps the task's harness, model and effort"},
		"native with effort":  {[]string{"--effort", "high", "--native"}, 2, false, "--native keeps the task's harness, model and effort"},
	} {
		t.Run(name, func(t *testing.T) {
			deps := testCommandRuntime(t)
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
			case test.exit == 0 && (got == nil || got.Native != test.native || got.Harness != "" || got.Model != "" || got.Effort != ""):
				t.Errorf("request = %+v, want Native with the harness, model and effort left as they are", got)
			}
		})
	}
}
