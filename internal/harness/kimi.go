package harness

import (
	"context"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

type kimiAdapter struct{}

func (kimiAdapter) Kind() Kind {
	return Kimi
}

func (kimiAdapter) Validate(ctx context.Context, runner execx.Runner) error {
	_, err := validateExecutable(ctx, runner, "kimi", "--version")
	return err
}

// Build produces a bare Kimi launch: Kimi rejects a positional brief as an
// unknown command, so the brief path is sent as a follow-up prompt after the
// composer is ready instead of being appended to the launch expression.
func (kimiAdapter) Build(spec LaunchSpec) (Launch, error) {
	launch, err := buildBase(spec)
	if err != nil {
		return Launch{}, err
	}
	launch.Executable = "kimi"
	if hasValue(spec.Model) {
		launch.Args = append(launch.Args, "--model", spec.Model)
	}
	// Kimi has no effort flag.
	launch.PromptFile = ""
	launch.FollowUpPrompt = "Read the brief at " + spec.BriefPath + " and follow it exactly."
	return launch, nil
}
