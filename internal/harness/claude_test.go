package harness

import (
	"context"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

func TestClaudeBuildsStructuredLaunch(t *testing.T) {
	registry := DefaultRegistry()
	adapter, err := registry.Get(Claude)
	if err != nil {
		t.Fatalf("Get(Claude): %v", err)
	}

	defaults, err := adapter.Build(LaunchSpec{BriefPath: `C:\briefs\task.md`, TaskTmp: `C:\tasks\task`})
	if err != nil {
		t.Fatalf("Build defaults: %v", err)
	}
	assertLaunch(t, defaults, Launch{
		Executable: "claude",
		Args:       []string{"--dangerously-skip-permissions"},
		Env: map[string]string{
			"CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION": "false",
			"GOTMPDIR":                             `C:\tasks\task\gotmp`,
		},
		PromptFile: `C:\briefs\task.md`,
		ConfirmMarkers: []string{
			"Is this a project you created or one you trust?",
			"Do you trust the files in this folder?",
		},
	})

	explicit, err := adapter.Build(LaunchSpec{
		BriefPath: `C:\briefs\task.md`,
		TaskTmp:   `C:\tasks\task`,
		Model:     "sonnet",
		Effort:    "xhigh",
	})
	if err != nil {
		t.Fatalf("Build explicit: %v", err)
	}
	if got, want := explicit.Args, []string{"--dangerously-skip-permissions", "--model", "sonnet", "--effort", "xhigh"}; !equalStrings(got, want) {
		t.Errorf("Args = %#v, want %#v", got, want)
	}

	if _, err := adapter.Build(LaunchSpec{BriefPath: `C:\briefs\task.md`, TaskTmp: `C:\tasks\task`, Effort: "turbo"}); err == nil {
		t.Fatal("Build returned nil error for unsupported effort")
	}
}

func TestClaudeValidateChecksExecutable(t *testing.T) {
	registry := DefaultRegistry()
	adapter, err := registry.Get(Claude)
	if err != nil {
		t.Fatalf("Get(Claude): %v", err)
	}
	runner := &fakeRunner{}
	if err := adapter.Validate(context.Background(), runner); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	assertRequests(t, runner.requests, []execx.Request{{Name: "claude", Args: []string{"--version"}}})
}
