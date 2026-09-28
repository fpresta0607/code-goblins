package harness

import (
	"context"
	"slices"
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

	defaults, err := adapter.Build(LaunchSpec{BriefPath: `C:\briefs\task.md`, TaskTmp: `C:\tasks\task`, GoTmp: `C:\gotmp\task`})
	if err != nil {
		t.Fatalf("Build defaults: %v", err)
	}
	assertLaunch(t, defaults, Launch{
		Args:           []string{"--dangerously-bypass-approvals-and-sandbox", "-c", "check_for_update_on_startup=false"},
		Env:            map[string]string{"CFO_ROLE": RoleGoblin, "GOTMPDIR": `C:\gotmp\task`},
		PromptFile:     `C:\briefs\task.md`,
		TypedLaunch:    true,
		Executable:     "codex",
		ConfirmMarkers: []string{"Do you trust the contents of this directory?"},
		ConfirmKeys:    []string{"enter"},
	})

	explicit, err := adapter.Build(LaunchSpec{
		BriefPath:       `C:\briefs\task.md`,
		TaskTmp:         `C:\tasks\task`,
		GoTmp:           `C:\gotmp\task`,
		TurnEndedPath:   `C:\tasks\task\turn-ended`,
		Model:           "gpt-5.2-codex",
		Effort:          "high",
		PiExtensionPath: `C:\ignored\pi.ts`,
	})
	if err != nil {
		t.Fatalf("Build explicit: %v", err)
	}
	wantArgs := []string{"--dangerously-bypass-approvals-and-sandbox", "-c", "check_for_update_on_startup=false", "--model", "gpt-5.2-codex", "-c", `model_reasoning_effort=high`}
	if !equalStrings(explicit.Args, wantArgs) {
		t.Errorf("Args = %#v, want %#v", explicit.Args, wantArgs)
	}
	for _, arg := range explicit.Args {
		if strings.Contains(arg, "notify=") || strings.Contains(arg, "bash") || strings.Contains(arg, "turn-ended") {
			t.Errorf("Args contained unsupported Bash notification: %#v", explicit.Args)
		}
	}

	if _, err := adapter.Build(LaunchSpec{BriefPath: `C:\briefs\task.md`, TaskTmp: `C:\tasks\task`, GoTmp: `C:\gotmp\task`, Effort: "invalid"}); err == nil {
		t.Fatal("Build returned nil error for invalid effort")
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

func TestCodexMaxEffortForAstra(t *testing.T) {
	adapter, _ := DefaultRegistry().Get(Codex)
	launch, err := adapter.Build(LaunchSpec{BriefPath: `C:\briefs\task.md`, TaskTmp: `C:\tasks\task`, GoTmp: `C:\gotmp\task`, Model: "gpt-6-astra", Effort: "max"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--dangerously-bypass-approvals-and-sandbox", "-c", "check_for_update_on_startup=false", "--model", "gpt-6-astra", "-c", "model_reasoning_effort=max"}
	if !equalStrings(launch.Args, want) {
		t.Fatalf("Args = %q, want %q", launch.Args, want)
	}
}

// On 2026-09-28 a Codex goblin never started: Codex 0.154.0 knew of 0.157.1,
// so it opened its "Update available!" prompt before its composer, the spawn
// typed the goblin's brief into that prompt, a key in it left Codex, and the
// rest of the brief ran in PowerShell. A goblin never needs that prompt, so
// Codex starts without checking for an update, on either backend.
func TestCodexStartsWithoutCheckingForAnUpdate(t *testing.T) {
	adapter, _ := DefaultRegistry().Get(Codex)

	launch, err := adapter.Build(LaunchSpec{BriefPath: `C:\briefs\task.md`, TaskTmp: `C:\tasks\task`, GoTmp: `C:\gotmp\task`})

	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(launch.Args, "check_for_update_on_startup=false") {
		t.Errorf("Args = %q, want Codex's startup update check off", launch.Args)
	}
}
