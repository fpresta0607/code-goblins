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
	// A goblin draws in Claude's classic interface, whatever the operator's
	// tui setting: its output stays in the terminal's own scrollback, which
	// the board scrolls locally, while the fullscreen interface repaints its
	// transcript for every turn of the wheel, about once a second in a long
	// working session.
	launch.Env["CLAUDE_CODE_DISABLE_ALTERNATE_SCREEN"] = "1"
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
		// /exit with background work running opens Claude's "Background
		// work is running" menu. Its first and highlighted option is Exit
		// and stop tasks, which also ends the background work, so Enter
		// picks it.
		ExitMarkers: []string{"Background work is running"},
		ExitKeys:    []string{"Enter"},
	}
}
