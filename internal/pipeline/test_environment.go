package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/project"
)

// pythonProbeLimit bounds only an interpreter that hangs. Under memory
// pressure Windows pages out a venv's interpreter and its DLLs, and a correct
// cold start was measured at 20.3 s (issue 2032), so the limit leaves room
// for several times that.
const pythonProbeLimit = 2 * time.Minute

const pythonEnvironmentProbe = `import json,sys; print(json.dumps({"executable":sys.executable,"prefix":sys.prefix,"base_prefix":sys.base_prefix,"version":".".join(map(str,sys.version_info[:3])),"releaselevel":sys.version_info.releaselevel,"serial":sys.version_info.serial}))`

// ProjectTestEnvironment binds only the registered source task's existing
// .venv. The daemon's wrapper calls it again after admission, before starting
// the unchanged repository command. It never consults a client's UV settings.
func ProjectTestEnvironment(ctx context.Context, commands execx.Runner, data, checkout, worktree string) ([]string, error) {
	manifest, path, err := project.LoadForCheckout(data, checkout)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("pipeline: test environment: repair %s: %w", path, err)
	}
	if manifest.TestEnvironment == nil {
		return nil, nil
	}
	refuse := func(reason string) ([]string, error) {
		return nil, fmt.Errorf("pipeline: required test environment: %s; verify the registered task's existing .venv and python_version in %s; no dependency installation or command execution was attempted", reason, path)
	}
	if !strings.EqualFold(manifest.Project, filepath.Base(checkout)) {
		return refuse("project manifest identity does not match the gate's source project")
	}
	source, err := fsx.Canonical(worktree)
	if err != nil {
		return refuse("registered source worktree is unavailable")
	}
	environment, err := fsx.Canonical(filepath.Join(source, ".venv"))
	if err != nil || !fsx.SamePath(filepath.Dir(environment), source) {
		return refuse(".venv is missing or resolves outside the registered source worktree")
	}
	if _, err := fsx.ReadFile(filepath.Join(environment, "pyvenv.cfg")); err != nil {
		return refuse(".venv/pyvenv.cfg is unavailable")
	}
	python := filepath.Join(environment, "bin", "python")
	if runtime.GOOS == "windows" {
		python = filepath.Join(environment, "Scripts", "python.exe")
	}
	if info, err := os.Stat(python); err != nil || !info.Mode().IsRegular() {
		return refuse("the existing environment's Python interpreter is unavailable")
	}
	probeContext, cancel := context.WithTimeout(ctx, pythonProbeLimit)
	defer cancel()
	result, err := commands.Run(probeContext, execx.Request{Dir: source, Name: python, Args: []string{"-I", "-c", pythonEnvironmentProbe}, KillTree: true})
	if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
		return nil, fmt.Errorf("pipeline: required test environment: the existing Python interpreter's isolated identity probe timed out after %s, so the environment was not judged; a machine short of memory can start an interpreter this slowly, and a later run or reattach can succeed once memory is free; no dependency installation or command execution was attempted", pythonProbeLimit)
	}
	if err != nil || result.ExitCode != 0 {
		return refuse("the existing Python interpreter could not complete its isolated identity probe")
	}
	var identity struct {
		Executable   string `json:"executable"`
		Prefix       string `json:"prefix"`
		BasePrefix   string `json:"base_prefix"`
		Version      string `json:"version"`
		ReleaseLevel string `json:"releaselevel"`
		Serial       *int   `json:"serial"`
	}
	if err := json.Unmarshal(result.Stdout, &identity); err != nil {
		return refuse("Python identity probe returned invalid JSON")
	}
	base, baseError := fsx.Canonical(identity.BasePrefix)
	if identity.Version != manifest.TestEnvironment.PythonVersion || identity.ReleaseLevel != "final" || identity.Serial == nil || *identity.Serial != 0 || baseError != nil ||
		!filepath.IsAbs(identity.Executable) || !filepath.IsAbs(identity.Prefix) || !filepath.IsAbs(identity.BasePrefix) ||
		!fsx.SamePath(identity.Executable, python) || !fsx.SamePath(identity.Prefix, environment) || fsx.SamePath(base, environment) {
		return refuse("Python executable, environment prefix or exact version does not match the required binding")
	}
	return []string{"UV_PROJECT_ENVIRONMENT=" + environment, "UV_PYTHON=" + python, "UV_NO_SYNC=1", "UV_OFFLINE=1", "UV_PYTHON_DOWNLOADS=never"}, nil
}

// CheckTestEnvironmentWrapper prevents a managed run from starting when its
// daemon would omit the binding, including in temporary base checkouts.
func CheckTestEnvironmentWrapper(root string) error {
	data, err := fsx.ReadFile(filepath.Join(root, "config.yaml"))
	if err != nil {
		return err
	}
	document, err := parseYAML(data)
	if err != nil {
		return err
	}
	var config struct {
		Wrapper struct {
			Command []string `yaml:"command"`
		} `yaml:"test_command_wrapper"`
	}
	if err := document.Decode(&config); err != nil {
		return err
	}
	words := config.Wrapper.Command
	if len(words) >= 8 && words[1] == "gate" && words[2] == "turn" && words[len(words)-1] == "--" {
		hasRun, hasComparison := false, false
		for at := 3; at+1 < len(words)-1; at++ {
			if words[at] == "--gate-run" && words[at+1] == "{run}" {
				hasRun = true
			}
			if words[at] == "--test-comparison-sha" && words[at+1] == "{test_comparison_sha}" {
				hasComparison = true
			}
		}
		if hasRun && hasComparison {
			return nil
		}
	}
	return errors.New("pipeline: required test environment: configure test_command_wrapper.command with the reviewed goblins binary, gate turn, --gate-run {run}, --test-comparison-sha {test_comparison_sha}, then --; the daemon must support the resolved Test comparison contract and bind both head and base commands before a run starts")
}
