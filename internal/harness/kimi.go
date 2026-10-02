package harness

import (
	"context"
	"fmt"

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

// Build produces a bare Kimi launch: the brief is typed into its composer like
// every harness's, so no positional prompt is appended. Kimi has no native
// screens yet, so no spawn or switch starts it until they are captured live.
func (kimiAdapter) Build(spec LaunchSpec) (Launch, error) {
	launch, err := buildBase(spec)
	if err != nil {
		return Launch{}, err
	}
	if hasValue(spec.Model) {
		launch.Args = append(launch.Args, "--model", spec.Model)
	}
	// Kimi has no effort flag, so an effort the harness cannot honour must
	// not be silently recorded in metadata.
	if hasValue(spec.Effort) {
		return Launch{}, fmt.Errorf("harness: Kimi does not support effort %q", spec.Effort)
	}
	return launch, nil
}

// Control stops Kimi with /quit after an Escape, because a streaming Kimi
// ignores a typed command until the stream is interrupted. Kimi's --continue
// resumes the previous session for the working directory; its -S/--session
// form needs an id cfo does not record, and an in-place switch never leaves
// the worktree, so --continue is the resume that fits.
func (kimiAdapter) Control() Control {
	return Control{
		StopKeys:    []string{"escape"},
		StopCommand: "/quit",
		ResumeArgs:  []string{"--continue"},
	}
}
