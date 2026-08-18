package harness

import (
	"context"
	"fmt"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

type claudeAdapter struct{}

func (claudeAdapter) Kind() Kind {
	return Claude
}

func (claudeAdapter) Validate(ctx context.Context, runner execx.Runner) error {
	_, err := validateExecutable(ctx, runner, "claude", "--version")
	return err
}

func (claudeAdapter) Build(spec LaunchSpec) (Launch, error) {
	launch, err := buildBase(spec)
	if err != nil {
		return Launch{}, err
	}
	launch.Env["CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION"] = "false"
	// Goblin panes must not inherit the operator's connected claude.ai MCP
	// servers: those are interactive-auth (OAuth) servers that print "N MCP
	// servers need authentication - run /mcp" on every launch and do the goblin
	// no good. --strict-mcp-config with no --mcp-config starts Claude with no
	// MCP servers at all; project credentials ride in through the injected
	// environment instead.
	launch.Args = []string{"--dangerously-skip-permissions", "--strict-mcp-config"}
	// Fresh treehouse worktrees are never in ~/.claude.json, so interactive
	// Claude launches open the workspace trust dialog. herdr agent start
	// returns success while Claude sits at that dialog and the agent reports
	// blocked; the dialog highlights "Yes, I trust this folder" by default,
	// so a single Enter confirms it once the marker text is on screen.
	launch.ConfirmMarkers = []string{
		"Is this a project you created or one you trust?",
		"Do you trust the files in this folder?",
	}
	launch.ConfirmKeys = []string{"enter"}
	if hasValue(spec.Model) {
		launch.Args = append(launch.Args, "--model", spec.Model)
	}
	if hasValue(spec.Effort) {
		if !validSharedEffort(spec.Effort) {
			return Launch{}, fmt.Errorf("harness: Claude does not support effort %q", spec.Effort)
		}
		launch.Args = append(launch.Args, "--effort", spec.Effort)
	}
	return launch, nil
}

// Control stops Claude with its own /exit command. --continue resumes the
// most recent conversation in the working directory, which is exactly the
// scope of an in-place switch, so no session id has to be tracked.
func (claudeAdapter) Control() Control {
	return Control{
		StopKeys:    []string{"escape"},
		StopCommand: "/exit",
		ResumeArgs:  []string{"--continue"},
	}
}
