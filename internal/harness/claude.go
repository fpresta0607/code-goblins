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
	launch.Executable = "claude"
	launch.Env["CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION"] = "false"
	launch.Args = []string{"--dangerously-skip-permissions"}
	// Fresh treehouse worktrees are never in ~/.claude.json, so interactive
	// Claude launches open the workspace trust dialog and report blocked until
	// it is confirmed. The dialog has no typed Herdr or CLI bypass; spawn
	// confirms it with Enter when the marker text is on screen.
	launch.ConfirmMarkers = []string{
		"Is this a project you created or one you trust?",
		"Do you trust the files in this folder?",
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
