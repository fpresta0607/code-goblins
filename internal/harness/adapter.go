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
	Kimi   Kind = "kimi"
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

// Launch is a harness launch specification. By default Herdr starts the
// harness itself through `herdr agent start` with Args after `--`, so the
// agent is named and registered from birth; agent start has no environment or
// working-directory support, so Env and Dir render the typed PowerShell prefix
// that prepares the pane shell first. On that native path the brief is never
// embedded in a shell line: PromptFile is referenced by path in the
// instruction submitted through `herdr agent prompt` once the agent is ready.
// ConfirmMarkers mark a blocking harness startup dialog (the workspace trust
// prompt claude, kimi, and pi show in every fresh worktree): while a marker is on
// screen, spawn sends ConfirmKeys to confirm the dialog. The keys differ per
// harness because the default-highlighted option differs.
// TypedLaunch is the fallback for harnesses Herdr cannot start natively
// (Herdr's Windows agent start uses Start-Process -FilePath, which cannot
// execute npm .cmd shims like pi): the full command plus the brief instruction
// is typed into the prepared pane shell instead, and Herdr detects the agent.
// SecretsFile, when set, is dot-sourced by the prefix instead of the values
// being typed into the pane. A credential typed inline would sit in the
// pane's scrollback and in every `cfo peek`, so the pane only ever sees the
// path.
type Launch struct {
	Args           []string
	Env            map[string]string
	SecretsFile    string
	PromptFile     string
	Dir            string
	ConfirmMarkers []string
	ConfirmKeys    []string
	TypedLaunch    bool
	Executable     string
}

// BriefInstruction is the single prompt every goblin receives, delivered
// through `herdr agent prompt` on the native path or as the final typed
// argument on the typed-launch path.
func BriefInstruction(promptFile string) string {
	return "Read the brief at " + promptFile + " and follow it exactly."
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
		Kimi:   kimiAdapter{},
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
