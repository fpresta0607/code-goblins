// Package harness builds validated Windows launch specifications for the
// interactive harnesses supported by Plan 3.
package harness

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// Kind identifies one supported interactive harness.
type Kind string

const (
	Claude Kind = "claude"
	Codex  Kind = "codex"
	Pi     Kind = "pi"
)

// LaunchSpec contains the task-specific values used to build one harness
// launch. TurnEndedPath is retained for the orchestration contract but is not
// used by a Plan 3 Windows adapter.
type LaunchSpec struct {
	BriefPath       string
	TaskTmp         string
	TurnEndedPath   string
	Model           string
	Effort          string
	PiExtensionPath string
}

// Launch is a shell-independent command specification. PromptFile is read by
// the final PowerShell delivery expression, rather than copied into Args.
type Launch struct {
	Executable string
	Args       []string
	Env        map[string]string
	PromptFile string
}

// Adapter builds and validates one supported harness launch.
type Adapter interface {
	Kind() Kind
	Validate(ctx context.Context, runner execx.Runner) error
	Build(spec LaunchSpec) (Launch, error)
}

// Registry contains the known typed adapters.
type Registry struct {
	Adapters map[Kind]Adapter
}

// DefaultRegistry provides precisely the Plan 3 harness set.
func DefaultRegistry() Registry {
	return Registry{Adapters: map[Kind]Adapter{
		Claude: claudeAdapter{},
		Codex:  codexAdapter{},
		Pi:     &piAdapter{},
	}}
}

// Get returns a supported typed adapter. Raw commands and excluded harnesses
// have no adapter in the Windows-native Plan 3 contract.
func (r Registry) Get(kind Kind) (Adapter, error) {
	adapter, ok := r.Adapters[kind]
	if !ok || adapter == nil {
		return nil, fmt.Errorf("harness: unsupported harness %q", kind)
	}
	return adapter, nil
}

func buildBase(spec LaunchSpec) (Launch, error) {
	if strings.TrimSpace(spec.BriefPath) == "" || !filepath.IsAbs(spec.BriefPath) {
		return Launch{}, errors.New("harness: BriefPath must be absolute")
	}
	if strings.TrimSpace(spec.TaskTmp) == "" || !filepath.IsAbs(spec.TaskTmp) {
		return Launch{}, errors.New("harness: TaskTmp must be absolute")
	}
	return Launch{
		Env:        map[string]string{"GOTMPDIR": filepath.Join(spec.TaskTmp, "gotmp")},
		PromptFile: spec.BriefPath,
	}, nil
}

func hasValue(value string) bool {
	return value != "" && value != "default"
}

func validSharedEffort(effort string) bool {
	switch effort {
	case "low", "medium", "high", "xhigh", "max":
		return true
	default:
		return false
	}
}

func validateExecutable(ctx context.Context, runner execx.Runner, executable string, args ...string) (execx.Result, error) {
	if runner == nil {
		return execx.Result{}, errors.New("harness: command runner is required")
	}
	result, err := runner.Run(ctx, execx.Request{Name: executable, Args: args})
	if err != nil {
		return execx.Result{}, fmt.Errorf("harness: validate %s: %w", executable, err)
	}
	if result.ExitCode != 0 {
		stderr := strings.TrimSpace(string(result.Stderr))
		if stderr == "" {
			return execx.Result{}, fmt.Errorf("harness: validate %s exited with code %d", executable, result.ExitCode)
		}
		return execx.Result{}, fmt.Errorf("harness: validate %s exited with code %d: %s", executable, result.ExitCode, stderr)
	}
	return result, nil
}
