package harness

import (
	"context"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

func TestKimiBuildsNativeLaunchWithTrustConfirm(t *testing.T) {
	registry := DefaultRegistry()
	adapter, err := registry.Get(Kimi)
	if err != nil {
		t.Fatalf("Get(Kimi): %v", err)
	}

	defaults, err := adapter.Build(LaunchSpec{BriefPath: `C:\briefs\task.md`, TaskTmp: `C:\tasks\task`})
	if err != nil {
		t.Fatalf("Build defaults: %v", err)
	}
	assertLaunch(t, defaults, Launch{
		Env: map[string]string{
			"CFO_ROLE": RoleGoblin,
			"GOTMPDIR": `C:\tasks\task\gotmp`,
		},
		PromptFile:     `C:\briefs\task.md`,
		ConfirmMarkers: []string{"Trust this folder?"},
		ConfirmKeys:    []string{"up", "enter"},
	})

	explicit, err := adapter.Build(LaunchSpec{
		BriefPath: `C:\briefs\task.md`,
		TaskTmp:   `C:\tasks\task`,
		Model:     "kimi-code/k3",
	})
	if err != nil {
		t.Fatalf("Build explicit: %v", err)
	}
	if got, want := explicit.Args, []string{"--model", "kimi-code/k3"}; !equalStrings(got, want) {
		t.Errorf("Args = %#v, want %#v", got, want)
	}

	if _, err := adapter.Build(LaunchSpec{BriefPath: `C:\briefs\task.md`, TaskTmp: `C:\tasks\task`, Effort: "xhigh"}); err == nil || !strings.Contains(err.Error(), "does not support effort") {
		t.Fatalf("err = %v, want a refusal naming the unsupported effort", err)
	}
}

func TestKimiValidateChecksExecutable(t *testing.T) {
	registry := DefaultRegistry()
	adapter, err := registry.Get(Kimi)
	if err != nil {
		t.Fatalf("Get(Kimi): %v", err)
	}
	runner := &fakeRunner{}
	if err := adapter.Validate(context.Background(), runner); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	assertRequests(t, runner.requests, []execx.Request{{Name: "kimi", Args: []string{"--version"}}})
}
