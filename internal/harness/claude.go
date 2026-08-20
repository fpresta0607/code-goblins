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
	// no good. --strict-mcp-config restricts Claude to the configs named with
	// --mcp-config, and spawn hands it exactly one: the token-authenticated
	// subset of the project's own .mcp.json, materialized under the task's
	// temporary directory rather than inside the checkout. OAuth connectors
	// are filtered out by construction, because a goblin can never complete
	// their browser flow. Claude is the only adapter that reads
	// LaunchSpec.MCPConfig, so this filter covers claude goblins alone.
	// Project credentials ride in through the injected environment instead.
	launch.Args = []string{"--dangerously-skip-permissions", "--strict-mcp-config"}
	if hasValue(spec.MCPConfig) {
		launch.Args = append(launch.Args, "--mcp-config", spec.MCPConfig)
	}
	// Fresh worktrees are never in ~/.claude.json, so interactive
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
		// Resuming a session that sat idle past its prompt-cache lifetime
		// opens Claude's resume dialog. Summary is its highlighted default
		// and the right choice for a goblin, so the standard confirm Enter
		// accepts it once a marker is on screen. The markers are the
		// dialog's static explanation sentence rather than its option
		// labels ("Resume from summary" and friends): markers are matched as
		// substrings of the pane tail, and --continue replays the prior
		// conversation into that same tail, so an option label the goblin
		// happened to discuss would hold the dialog loop open forever.
		ResumeMarkers: []string{
			"will consume a substantial portion of your usage limits",
			"We recommend resuming from a summary",
		},
	}
}
