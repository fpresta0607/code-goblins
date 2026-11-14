package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
)

func TestProjectTestEnvironmentVerifiesTheInterpreterAndReturnsOnlyFixedUVSettings(t *testing.T) {
	// Arrange
	data, checkout, worktree, python := existingEnvironmentFixture(t)
	t.Setenv("UV_PROJECT_ENVIRONMENT", t.TempDir())
	t.Setenv("ARBITRARY_CLIENT_TOKEN", "client-only-sentinel")
	serial := 0
	var requests []execx.Request
	commands := runnerFunc(func(_ context.Context, request execx.Request) (execx.Result, error) {
		requests = append(requests, request)
		return execx.Result{Stdout: environmentIdentity(t, python, filepath.Dir(filepath.Dir(python)), checkout, "3.12.10", "final", &serial)}, nil
	})

	// Act
	variables, err := ProjectTestEnvironment(context.Background(), commands, data, checkout, worktree)

	// Assert
	want := []string{"UV_PROJECT_ENVIRONMENT=" + filepath.Dir(filepath.Dir(python)), "UV_PYTHON=" + python, "UV_NO_SYNC=1", "UV_OFFLINE=1", "UV_PYTHON_DOWNLOADS=never"}
	if err != nil || !slices.Equal(variables, want) || len(requests) != 1 {
		t.Fatalf("variables=%q requests=%v error=%v", variables, requests, err)
	}
	request := requests[0]
	if request.Name != python || request.Dir != worktree || len(request.Args) != 3 || request.Args[0] != "-I" || request.Args[1] != "-c" || !request.KillTree || request.Env != nil {
		t.Fatalf("probe did not run the selected interpreter in isolation: %+v", request)
	}
	if os.Getenv("ARBITRARY_CLIENT_TOKEN") != "client-only-sentinel" || os.Getenv("UV_PROJECT_ENVIRONMENT") == filepath.Dir(filepath.Dir(python)) {
		t.Fatal("binding changed its caller's environment")
	}
}

func TestProjectTestEnvironmentRefusesInvalidOrUnavailableBindings(t *testing.T) {
	for _, scenario := range []string{"missing venv", "missing config", "missing interpreter", "wrong version", "wrong executable", "wrong prefix", "not a venv", "invalid output", "probe failure", "probe unavailable", "wrong project", "invalid manifest"} {
		t.Run(scenario, func(t *testing.T) {
			// Arrange
			data, checkout, worktree, python := existingEnvironmentFixture(t)
			prefix, executable, version := filepath.Dir(filepath.Dir(python)), python, "3.12.10"
			serial := 0
			var hasProbed bool
			switch scenario {
			case "missing venv":
				if err := os.Rename(prefix, prefix+"-unavailable"); err != nil {
					t.Fatal(err)
				}
			case "missing config":
				if err := os.Remove(filepath.Join(prefix, "pyvenv.cfg")); err != nil {
					t.Fatal(err)
				}
			case "missing interpreter":
				if err := os.Remove(python); err != nil {
					t.Fatal(err)
				}
			case "wrong version":
				version = "3.14.3"
			case "wrong executable":
				executable = filepath.Join(checkout, "other-python")
			case "wrong prefix":
				prefix = checkout
			case "wrong project":
				writeEnvironmentManifest(t, data, checkout, `{"project":"another-project","test_environment":{"python_version":"3.12.10"}}`)
			case "invalid manifest":
				writeEnvironmentManifest(t, data, checkout, `{"project":"project","test_environment":{"python_version":"3.12.10","env":{"CLIENT_TOKEN":"not-supported"}}}`)
			}
			commands := runnerFunc(func(_ context.Context, request execx.Request) (execx.Result, error) {
				hasProbed = true
				base := checkout
				if scenario == "not a venv" {
					base = prefix
				}
				if scenario == "probe failure" {
					return execx.Result{ExitCode: 1}, nil
				}
				if scenario == "probe unavailable" {
					return execx.Result{}, errors.New("unavailable")
				}
				if scenario == "invalid output" {
					return execx.Result{Stdout: []byte("not JSON")}, nil
				}
				return execx.Result{Stdout: environmentIdentity(t, executable, prefix, base, version, "final", &serial)}, nil
			})

			// Act
			variables, err := ProjectTestEnvironment(context.Background(), commands, data, checkout, worktree)

			// Assert
			if err == nil || variables != nil || !strings.Contains(err.Error(), "test environment") {
				t.Fatalf("variables=%q error=%v; want refusal", variables, err)
			}
			if strings.HasPrefix(scenario, "missing") && hasProbed {
				t.Fatal("started a probe for a missing environment")
			}
		})
	}
}

// A probe the runner cut at its context's deadline returns that deadline's
// error, as execx.OSRunner does, so a slow probe is simulated without waiting.
func slowProbe(t *testing.T, duration time.Duration, python, checkout string) runnerFunc {
	serial := 0
	return func(ctx context.Context, _ execx.Request) (execx.Result, error) {
		if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < duration {
			return execx.Result{}, &execx.Ended{Err: context.DeadlineExceeded}
		}
		return execx.Result{Stdout: environmentIdentity(t, python, filepath.Dir(filepath.Dir(python)), checkout, "3.12.10", "final", &serial)}, nil
	}
}

func TestProjectTestEnvironmentAcceptsAnInterpreterSlowToStartUnderMemoryPressure(t *testing.T) {
	// Arrange: issue 2032 measured a cold start of 20.3 s on a loaded machine.
	data, checkout, worktree, python := existingEnvironmentFixture(t)

	// Act
	variables, err := ProjectTestEnvironment(t.Context(), slowProbe(t, 20300*time.Millisecond, python, checkout), data, checkout, worktree)

	// Assert
	if err != nil || len(variables) == 0 {
		t.Fatalf("variables=%q error=%v; want a correct environment bound after a slow cold start", variables, err)
	}
}

func TestProjectTestEnvironmentReportsAProbeTimeoutAsATimeout(t *testing.T) {
	// Arrange
	data, checkout, worktree, python := existingEnvironmentFixture(t)

	// Act
	variables, err := ProjectTestEnvironment(t.Context(), slowProbe(t, time.Hour, python, checkout), data, checkout, worktree)

	// Assert
	if err == nil || variables != nil {
		t.Fatalf("variables=%q error=%v; want a timeout refusal", variables, err)
	}
	if message := err.Error(); !strings.Contains(message, "timed out after 2m0s") || strings.Contains(message, ".venv") {
		t.Fatalf("error=%v; want the timeout and how long it waited, not the environment blamed", err)
	}
}

func TestProjectTestEnvironmentDoesNotReportAnEndedCallerAsAProbeTimeout(t *testing.T) {
	// Arrange
	data, checkout, worktree, python := existingEnvironmentFixture(t)
	caller, cancel := context.WithDeadline(t.Context(), time.Now())
	defer cancel()

	// Act
	variables, err := ProjectTestEnvironment(caller, slowProbe(t, time.Hour, python, checkout), data, checkout, worktree)

	// Assert
	if err == nil || variables != nil || !strings.Contains(err.Error(), "required test environment") {
		t.Fatalf("variables=%q error=%v; want a refusal", variables, err)
	}
	if strings.Contains(err.Error(), "timed out after") {
		t.Fatalf("error=%v; the caller's own deadline ended the probe, not the probe's limit", err)
	}
}

func TestProjectTestEnvironmentRequiresTheExactFinalPythonRelease(t *testing.T) {
	zero, positive, negative := 0, 3, -1
	for _, identity := range []struct {
		name, version, releaseLevel string
		serial                      *int
		isValid                     bool
	}{
		{"stable final", "3.12.10", "final", &zero, true},
		{"alpha", "3.14.0", "alpha", &positive, false},
		{"beta", "3.14.0", "beta", &positive, false},
		{"release candidate", "3.14.0", "candidate", &positive, false},
		{"nonzero final serial", "3.12.10", "final", &positive, false},
		{"negative final serial", "3.12.10", "final", &negative, false},
		{"empty release level", "3.12.10", "", &zero, false},
		{"null serial", "3.12.10", "final", nil, false},
	} {
		t.Run(identity.name, func(t *testing.T) {
			data, checkout, worktree, python := existingEnvironmentFixture(t)
			writeEnvironmentManifest(t, data, checkout, `{"project":"`+filepath.Base(checkout)+`","test_environment":{"python_version":"`+identity.version+`"}}`)
			commands := runnerFunc(func(context.Context, execx.Request) (execx.Result, error) {
				return execx.Result{Stdout: environmentIdentity(t, python, filepath.Dir(filepath.Dir(python)), checkout, identity.version, identity.releaseLevel, identity.serial)}, nil
			})

			variables, err := ProjectTestEnvironment(t.Context(), commands, data, checkout, worktree)

			if identity.isValid {
				want := []string{"UV_PROJECT_ENVIRONMENT=" + filepath.Dir(filepath.Dir(python)), "UV_PYTHON=" + python, "UV_NO_SYNC=1", "UV_OFFLINE=1", "UV_PYTHON_DOWNLOADS=never"}
				if err != nil || !slices.Equal(variables, want) {
					t.Fatalf("variables=%q error=%v; want the exact final release bound", variables, err)
				}
			} else if err == nil || variables != nil || !strings.Contains(err.Error(), "exact version") {
				t.Fatalf("variables=%q error=%v; want invalid release identity refused", variables, err)
			}
		})
	}
}

func TestProjectTestEnvironmentDoesNotBorrowAnotherProjectsBinding(t *testing.T) {
	// Arrange
	data, checkout, worktree, _ := existingEnvironmentFixture(t)
	other := t.TempDir()
	commands := runnerFunc(func(context.Context, execx.Request) (execx.Result, error) {
		t.Fatal("another project's environment must not be probed")
		return execx.Result{}, nil
	})

	// Act
	variables, err := ProjectTestEnvironment(context.Background(), commands, data, other, worktree)

	// Assert
	if err != nil || variables != nil {
		t.Fatalf("unconfigured project inherited %s's binding: %q %v", checkout, variables, err)
	}
}

func existingEnvironmentFixture(t *testing.T) (data, checkout, worktree, python string) {
	t.Helper()
	data, checkout = t.TempDir(), t.TempDir()
	// A runner's temporary folder can be an 8.3 short name, and the binding
	// returns the long one it resolves, so the fixture names that one.
	worktree, err := fsx.Canonical(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	python = filepath.Join(worktree, ".venv", "bin", "python")
	if runtime.GOOS == "windows" {
		python = filepath.Join(worktree, ".venv", "Scripts", "python.exe")
	}
	if err := os.MkdirAll(filepath.Dir(python), 0o755); err != nil {
		t.Fatal(err)
	}
	for path, contents := range map[string]string{python: "fake interpreter", filepath.Join(worktree, ".venv", "pyvenv.cfg"): "home = existing\n"} {
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeEnvironmentManifest(t, data, checkout, `{"project":"`+filepath.Base(checkout)+`","test_environment":{"python_version":"3.12.10"}}`)
	return
}

func writeEnvironmentManifest(t *testing.T, data, checkout, contents string) {
	t.Helper()
	path := filepath.Join(data, "projects", filepath.Base(checkout), "project.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func environmentIdentity(t *testing.T, executable, prefix, base, version, releaseLevel string, serial *int) []byte {
	t.Helper()
	data, err := json.Marshal(struct {
		Executable   string `json:"executable"`
		Prefix       string `json:"prefix"`
		BasePrefix   string `json:"base_prefix"`
		Version      string `json:"version"`
		ReleaseLevel string `json:"releaselevel"`
		Serial       *int   `json:"serial"`
	}{executable, prefix, base, version, releaseLevel, serial})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestRequiredEnvironmentNeedsTheDaemonWrapperToCarryFullRunIdentity(t *testing.T) {
	for _, command := range []string{`["goblins", "gate", "turn", "--gate-run", "{run}", "--test-comparison-sha", "{test_comparison_sha}", "--"]`, `["goblins", "gate", "turn", "--gate-run", "{run}", "--"]`, `["goblins", "gate", "turn", "--"]`, `["goblins", "gate", "turn", "--gate-run", "{run_short}", "--test-comparison-sha", "{test_comparison_sha}", "--"]`, `["goblins", "gate", "turn", "--gate-run", "{run}", "--test-comparison-sha", "{run}", "--"]`, `[]`} {
		t.Run(command, func(t *testing.T) {
			// Arrange
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte("test_command_wrapper:\n  command: "+command+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			// Act
			err := CheckTestEnvironmentWrapper(root)
			// Assert
			isValid := command == `["goblins", "gate", "turn", "--gate-run", "{run}", "--test-comparison-sha", "{test_comparison_sha}", "--"]`
			if (err == nil) != isValid {
				t.Fatalf("command=%s error=%v", command, err)
			}
		})
	}
}
