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

// Build produces a bare Kimi launch: the brief arrives through
// `herdr agent prompt` like the other native-start harnesses, so no
// positional prompt is appended. Kimi shows its own workspace trust dialog in a fresh worktree but
// keeps reporting idle there (Herdr's kimi detection falls back to idle), and
// the dialog highlights "Don't trust" by default, so confirming it takes Up
// then Enter; a bare Enter would exit Kimi.
func (kimiAdapter) Build(spec LaunchSpec) (Launch, error) {
	launch, err := buildBase(spec)
	if err != nil {
		return Launch{}, err
	}
	if hasValue(spec.Model) {
		launch.Args = append(launch.Args, "--model", spec.Model)
	}
	// Kimi has no effort flag.
	launch.ConfirmMarkers = []string{"Trust this folder?"}
	launch.ConfirmKeys = []string{"up", "enter"}
	return launch, nil
}
