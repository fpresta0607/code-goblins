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
	BriefPath string
	TaskTmp   string
	// GoTmp is the directory GOTMPDIR points at, created by the caller and
	// deliberately outside the fleet checkout; see state.GoTmpDir.
	GoTmp           string
	TurnEndedPath   string
	Model           string
	Effort          string
	PiExtensionPath string
	// MCPConfig is the path of the goblin-safe MCP configuration (the
	// token-authenticated subset of the project's .mcp.json), materialized
	// under the task's temporary directory and empty when nothing qualified.
	// Only the claude adapter reads it, through --mcp-config; codex ignores
	// it and uses the operator's own codex configuration, and kimi has no
	// config flag and loads the copy provisioning leaves at the worktree root
	// when that path was safe to write.
	MCPConfig string
	// CodexMCPServers names the MCP servers the operator's own Codex
	// configuration defines, which the codex adapter turns off: a goblin
	// starts none of them, as claude's --strict-mcp-config starts none of the
	// operator's.
	CodexMCPServers []string
}

// Launch is a harness launch specification: the native terminal's host starts
// the harness with Args in Dir, with Env set. The brief is never embedded in
// the command line: PromptFile is referenced by path in the instruction typed
// into the harness's composer once it shows on screen. Executable, when set,
// names a harness installed as a script shim (the npm .cmd codex and pi install
// as), which the host starts through cmd /c; empty, the harness's own .exe
// starts.
type Launch struct {
	Args        []string
	Env         map[string]string
	PromptFile  string
	Instruction string
	Dir         string
	Executable  string
	// Resumed marks a launch that continues an existing session.
	Resumed bool
}

// PromptInstruction is the single instruction the harness receives once it is
// ready. It is the brief instruction unless a caller supplied its own, which
// is how an in-place switch hands the new harness a handoff instead.
func (launch Launch) PromptInstruction() string {
	if launch.Instruction != "" {
		return launch.Instruction
	}
	return BriefInstruction(launch.PromptFile)
}

// BriefInstruction is the single prompt every goblin receives once its
// harness is ready, typed into its composer.
func BriefInstruction(promptFile string) string {
	return "Read the brief at " + promptFile + " and follow it exactly."
}

// Control is how a running harness is stopped in place and, where it can,
// resumed. It exists so `cfo switch` can change a goblin's harness, model, or
// effort without tearing down its tab or worktree.
type Control struct {
	// StopKeys are sent before StopCommand. A harness mid-stream ignores a
	// typed slash command until the stream is interrupted.
	StopKeys []string
	// StopCommand is the harness's own exit command, typed and submitted.
	// Switch falls back to an interrupt when a harness does not exit on it,
	// so an imperfect command costs time rather than correctness.
	StopCommand string
	// ResumeArgs continue the harness's previous session in this working
	// directory, for a switch that only changes model or effort. They are
	// placed before the built launch arguments because codex takes its
	// resume as a subcommand. Empty means the harness cannot resume.
	ResumeArgs []string
	// ExitMarkers mark a dialog StopCommand can open instead of exiting
	// (claude asks what to do with background work still running). While
	// one is on screen, switch answers it with ExitKeys rather than
	// interrupting, which would leave that work running.
	ExitMarkers []string
	ExitKeys    []string
}

// RoleVariable names the pane role every launch stamps, and RoleGoblin is
// the value a goblin's pane carries. The CFO's Claude Code hooks read it and
// stay inert: a goblin is the work being supervised, never the supervisor.
const (
	RoleVariable = "CFO_ROLE"
	RoleGoblin   = "goblin"
)

// Adapter builds and validates one supported harness launch.
type Adapter interface {
	Kind() Kind
	Validate(ctx context.Context, runner execx.Runner) error
	Build(spec LaunchSpec) (Launch, error)
	// Control returns how to stop and resume this harness in place.
	Control() Control
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
	if strings.TrimSpace(spec.GoTmp) == "" || !filepath.IsAbs(spec.GoTmp) {
		return Launch{}, errors.New("harness: GoTmp must be absolute")
	}
	return Launch{
		Env: map[string]string{
			"GOTMPDIR": spec.GoTmp,
			// Every goblin pane is stamped with its role, and the CFO's
			// hooks read it to stay out of the way. It belongs in the launch
			// contract rather than in the project credentials a preflight
			// returns, because a project that declares no services returns
			// no credentials at all and would leave its goblin unstamped -
			// and an unstamped goblin is a goblin the CFO starts supervising
			// as if it were the CFO.
			RoleVariable: RoleGoblin,
		},
		PromptFile: spec.BriefPath,
	}, nil
}

func hasValue(value string) bool {
	return value != "" && value != "default"
}

// DefaultModel is the model a harness runs when no model is named. Every
// Claude goblin runs Opus 5.5, per the Supreme Overlord's directive of
// 2026-09-23; the other harnesses keep their own default.
func DefaultModel(kind Kind) string {
	if kind == Claude {
		return "claude-opus-5-5"
	}
	return ""
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
