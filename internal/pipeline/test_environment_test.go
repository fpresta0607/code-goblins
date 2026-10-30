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

	"github.com/fpresta0607/code-goblins/internal/execx"
)

func TestProjectTestEnvironmentVerifiesTheInterpreterAndReturnsOnlyFixedUVSettings(t *testing.T) {
	// Arrange
	data, checkout, worktree, python := existingEnvironmentFixture(t)
	t.Setenv("UV_PROJECT_ENVIRONMENT", t.TempDir())
	t.Setenv("ARBITRARY_CLIENT_TOKEN", "client-only-sentinel")
	var requests []execx.Request
	commands := runnerFunc(func(_ context.Context, request execx.Request) (execx.Result, error) {
		requests = append(requests, request)
		return execx.Result{Stdout: environmentIdentity(t, python, filepath.Dir(filepath.Dir(python)), checkout, "3.12.10")}, nil
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
				return execx.Result{Stdout: environmentIdentity(t, executable, prefix, base, version)}, nil
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
	data, checkout, worktree = t.TempDir(), t.TempDir(), t.TempDir()
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

func environmentIdentity(t *testing.T, executable, prefix, base, version string) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]string{"executable": executable, "prefix": prefix, "base_prefix": base, "version": version})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestRequiredEnvironmentNeedsTheDaemonWrapperToCarryFullRunIdentity(t *testing.T) {
	for _, command := range []string{`["cfo", "gate", "turn", "--gate-run", "{run}", "--"]`, `["cfo", "gate", "turn", "--"]`, `["cfo", "gate", "turn", "--gate-run", "{run_short}", "--"]`, `[]`} {
		t.Run(command, func(t *testing.T) {
			// Arrange
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte("test_command_wrapper:\n  command: "+command+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			// Act
			err := CheckTestEnvironmentWrapper(root)
			// Assert
			isValid := command == `["cfo", "gate", "turn", "--gate-run", "{run}", "--"]`
			if (err == nil) != isValid {
				t.Fatalf("command=%s error=%v", command, err)
			}
		})
	}
}
