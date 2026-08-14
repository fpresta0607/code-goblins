package harness

import (
	"context"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

func TestKimiBuildsBareLaunchWithFollowUpPrompt(t *testing.T) {
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
		Executable: "kimi",
		Env: map[string]string{
			"GOTMPDIR": `C:\tasks\task\gotmp`,
		},
		FollowUpPrompt: `Read the brief at C:\briefs\task.md and follow it exactly.`,
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
