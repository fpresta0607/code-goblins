package harness

import (
	"context"
	"fmt"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

type codexAdapter struct{}

func (codexAdapter) Kind() Kind {
	return Codex
}

func (codexAdapter) Validate(ctx context.Context, runner execx.Runner) error {
	_, err := validateExecutable(ctx, runner, "codex", "--version")
	return err
}

func (codexAdapter) Build(spec LaunchSpec) (Launch, error) {
	launch, err := buildBase(spec)
	if err != nil {
		return Launch{}, err
	}
	launch.Executable = "codex"
	launch.Args = []string{"--dangerously-bypass-approvals-and-sandbox"}
	if hasValue(spec.Model) {
		launch.Args = append(launch.Args, "--model", spec.Model)
	}
	if hasValue(spec.Effort) {
		switch spec.Effort {
		case "low", "medium", "high", "xhigh":
			launch.Args = append(launch.Args, "-c", `model_reasoning_effort="`+spec.Effort+`"`)
		default:
			return Launch{}, fmt.Errorf("harness: Codex does not support effort %q", spec.Effort)
		}
	}
	return launch, nil
}
