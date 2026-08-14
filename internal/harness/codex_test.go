package harness

import (
	"context"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

func TestCodexBuildsStructuredLaunchWithoutBashNotify(t *testing.T) {
	registry := DefaultRegistry()
	adapter, err := registry.Get(Codex)
	if err != nil {
		t.Fatalf("Get(Codex): %v", err)
	}

	defaults, err := adapter.Build(LaunchSpec{BriefPath: `C:\briefs\task.md`, TaskTmp: `C:\tasks\task`})
	if err != nil {
		t.Fatalf("Build defaults: %v", err)
	}
	assertLaunch(t, defaults, Launch{
		Executable: "codex",
		Args:       []string{"--dangerously-bypass-approvals-and-sandbox"},
		Env:        map[string]string{"GOTMPDIR": `C:\tasks\task\gotmp`},
		PromptFile: `C:\briefs\task.md`,
	})

	explicit, err := adapter.Build(LaunchSpec{
		BriefPath:       `C:\briefs\task.md`,
		TaskTmp:         `C:\tasks\task`,
		TurnEndedPath:   `C:\tasks\task\turn-ended`,
		Model:           "gpt-5.2-codex",
		Effort:          "high",
		PiExtensionPath: `C:\ignored\pi.ts`,
	})
	if err != nil {
		t.Fatalf("Build explicit: %v", err)
	}
	wantArgs := []string{"--dangerously-bypass-approvals-and-sandbox", "--model", "gpt-5.2-codex", "-c", `model_reasoning_effort="high"`}
	if !equalStrings(explicit.Args, wantArgs) {
		t.Errorf("Args = %#v, want %#v", explicit.Args, wantArgs)
	}
	for _, arg := range explicit.Args {
		if strings.Contains(arg, "notify=") || strings.Contains(arg, "bash") || strings.Contains(arg, "turn-ended") {
			t.Errorf("Args contained unsupported Bash notification: %#v", explicit.Args)
		}
	}

	if _, err := adapter.Build(LaunchSpec{BriefPath: `C:\briefs\task.md`, TaskTmp: `C:\tasks\task`, Effort: "max"}); err == nil {
		t.Fatal("Build returned nil error for unsupported max effort")
	}
}

func TestCodexValidateChecksExecutable(t *testing.T) {
	registry := DefaultRegistry()
	adapter, err := registry.Get(Codex)
	if err != nil {
		t.Fatalf("Get(Codex): %v", err)
	}
	runner := &fakeRunner{}
	if err := adapter.Validate(context.Background(), runner); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	assertRequests(t, runner.requests, []execx.Request{{Name: "codex", Args: []string{"--version"}}})
}
