package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/spawn"
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
			case test.exit == 0 && (got == nil || got.ID != "g4" || got.Model != "gpt-5"):
				t.Errorf("request = %+v, want g4 switched to gpt-5", got)
			}
		})
	}
}
