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

	defaults, err := adapter.Build(LaunchSpec{BriefPath: `C:\briefs\task.md`, TaskTmp: `C:\tasks\task`, Scratch: `C:\gotmp\task`})
	if err != nil {
		t.Fatalf("Build defaults: %v", err)
	}
	assertLaunch(t, defaults, Launch{
		Args: []string{"--dangerously-skip-permissions", "--strict-mcp-config"},
		Env: map[string]string{
			"CFO_ROLE":                             RoleGoblin,
			"CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION": "false",
			"GOTMPDIR":                             `C:\gotmp\task`,
			"TEMP":                                 `C:\gotmp\task`,
			"TMP":                                  `C:\gotmp\.tmp`,
			"TMPDIR":                               `C:\gotmp\task`,
		},
		PromptFile: `C:\briefs\task.md`,
	})

	// A goblin draws as the CFO does, in the interface the operator's tui
	// setting names: the Overlord, 2026-10-07, on goblins' terminals lacking
	// the CFO's jump to bottom and fixed input line, "it works in cfo but not
	// other goblins so make sure that you fix that".
	if value, isSet := defaults.Env["CLAUDE_CODE_DISABLE_ALTERNATE_SCREEN"]; isSet {
		t.Errorf("Env sets CLAUDE_CODE_DISABLE_ALTERNATE_SCREEN=%q, which keeps a goblin out of the fullscreen interface the CFO runs in", value)
	}

	explicit, err := adapter.Build(LaunchSpec{
		BriefPath: `C:\briefs\task.md`,
		TaskTmp:   `C:\tasks\task`,
		Scratch:   `C:\gotmp\task`,
		Model:     "sonnet",
		Effort:    "xhigh",
	})
	if err != nil {
		t.Fatalf("Build explicit: %v", err)
	}
	if got, want := explicit.Args, []string{"--dangerously-skip-permissions", "--strict-mcp-config", "--model", "sonnet", "--effort", "xhigh"}; !equalStrings(got, want) {
		t.Errorf("Args = %#v, want %#v", got, want)
	}

	if _, err := adapter.Build(LaunchSpec{BriefPath: `C:\briefs\task.md`, TaskTmp: `C:\tasks\task`, Scratch: `C:\gotmp\task`, Effort: "turbo"}); err == nil {
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
