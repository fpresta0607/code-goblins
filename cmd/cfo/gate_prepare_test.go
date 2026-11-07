package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestGatePrepareConfiguredCommandReadiesFrontendBeforeTest(t *testing.T) {
	// Arrange
	data, err := os.ReadFile(filepath.Join(repoRootFromCmdCFO(t), ".no-mistakes.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Commands struct {
			Prepare string `yaml:"prepare"`
		} `yaml:"commands"`
	}
	if err := yaml.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	dir := testStepModule(t, map[string]string{
		".gitignore":                 "frontend/node_modules/\n",
		"frontend/package.json":      `{"name":"board","version":"1.0.0"}`,
		"frontend/package-lock.json": `{"name":"board","lockfileVersion":3,"packages":{}}`,
	}, map[string]string{"frontend/board.ts": "changed\n"})
	t.Chdir(dir)
	runtime := standIn()
	installs := 0
	runtime.gateRun = func(command []string, where string, _ []string, _, _ io.Writer) (int, error) {
		installs++
		if !slices.Equal(command, []string{"npm", "ci"}) || where != filepath.Join(dir, "frontend") {
			t.Fatalf("installer = %q in %s", command, where)
		}
		return 0, os.MkdirAll(filepath.Join(where, "node_modules"), 0o755)
	}
	var stdout, stderr bytes.Buffer

	// Act: consume the configured native preparation command, before Test.
	command := strings.Fields(config.Commands.Prepare)
	if len(command) < 2 || command[0] != "cfo" {
		t.Fatalf("first Test would receive no prepared dependencies: prepare=%q", config.Commands.Prepare)
	}
	exit := runWithRuntime(command[1:], &stdout, &stderr, runtime)

	// Assert: the first Test boundary has its own dependencies without repair.
	_, readyErr := os.Stat(filepath.Join(dir, "frontend", "node_modules"))
	if exit != 0 || installs != 1 || readyErr != nil {
		t.Fatalf("first Test prerequisites: exit=%d installs=%d ready=%v stdout=%s stderr=%s", exit, installs, readyErr, &stdout, &stderr)
	}
}

func TestGatePrepareInstallsOnlyForFrontendChanges(t *testing.T) {
	for _, test := range []struct {
		name, file, content string
		wantInstalls        int
	}{
		{"frontend", "frontend/src/board.ts", "export const board = 2\n", 1},
		{"lock", "frontend/package-lock.json", `{"lockfileVersion":3,"packages":{}}`, 1},
		{"existing dependencies", "frontend/src/board.ts", "export const board = 2\n", 1},
		{"go", "a/a.go", "package a\nfunc A() int { return 2 }\n", 0},
		{"documentation", "README.md", "changed\n", 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			dir := testStepModule(t, map[string]string{
				".gitignore":                 "frontend/node_modules/\n",
				"frontend/package.json":      `{"name":"board","version":"1.0.0"}`,
				"frontend/package-lock.json": `{"name":"board","lockfileVersion":3,"packages":{}}`,
				"frontend/src/board.ts":      "export const board = 1\n",
			}, map[string]string{test.file: test.content})
			t.Chdir(dir)
			if test.name == "existing dependencies" {
				if err := os.MkdirAll(filepath.Join(dir, "frontend", "node_modules"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			runtime := standIn()
			installs := 0
			runtime.gateRun = func(command []string, where string, env []string, _, _ io.Writer) (int, error) {
				installs++
				if !slices.Equal(command, []string{"npm", "ci"}) || where != filepath.Join(dir, "frontend") {
					t.Fatalf("installer = %q in %s", command, where)
				}
				for _, value := range env {
					if strings.HasPrefix(value, "CFO_HOME=") || strings.HasPrefix(value, "CFO_STATE_OVERRIDE=") {
						t.Fatalf("installer inherited fleet environment: %s", value)
					}
				}
				return 0, os.MkdirAll(filepath.Join(where, "node_modules"), 0o755)
			}
			var stdout, stderr bytes.Buffer

			// Act
			exit := runWithRuntime([]string{"gate", "prepare"}, &stdout, &stderr, runtime)

			// Assert
			if exit != 0 || installs != test.wantInstalls {
				t.Fatalf("exit=%d installs=%d, want 0/%d; stdout=%s stderr=%s", exit, installs, test.wantInstalls, &stdout, &stderr)
			}
			_, err := os.Stat(filepath.Join(dir, "frontend", "node_modules"))
			if (err == nil) != (test.wantInstalls == 1) {
				t.Fatalf("own dependencies ready=%v, want %v", err == nil, test.wantInstalls == 1)
			}
		})
	}
}

func TestGatePreparePropagatesLockedInstallerFailures(t *testing.T) {
	for _, test := range []struct {
		name string
		exit int
		err  error
	}{
		{"invalid lock", 1, nil},
		{"missing lock", 1, nil},
		{"missing tooling", -1, errors.New("npm not found")},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			base := map[string]string{"frontend/package-lock.json": "invalid\n"}
			if test.name == "missing lock" {
				delete(base, "frontend/package-lock.json")
			}
			dir := testStepModule(t, base, map[string]string{"frontend/board.ts": "changed\n"})
			t.Chdir(dir)
			runtime := standIn()
			installs := 0
			runtime.gateRun = func(command []string, _ string, _ []string, _, stderr io.Writer) (int, error) {
				installs++
				if !slices.Equal(command, []string{"npm", "ci"}) {
					t.Fatalf("unlocked install: %q", command)
				}
				io.WriteString(stderr, "locked install refused\n")
				return test.exit, test.err
			}
			var stdout, stderr bytes.Buffer

			// Act
			exit := runWithRuntime([]string{"gate", "prepare"}, &stdout, &stderr, runtime)

			// Assert
			if exit == 0 || installs != 1 || !strings.Contains(stderr.String(), "locked install refused") {
				t.Fatalf("exit=%d installs=%d stdout=%s stderr=%s", exit, installs, &stdout, &stderr)
			}
		})
	}
}

func TestGatePrepareRefusesAnUnreadablePlanWithoutInstalling(t *testing.T) {
	// Arrange
	t.Chdir(t.TempDir())
	runtime := standIn()
	installs := 0
	runtime.gateRun = func([]string, string, []string, io.Writer, io.Writer) (int, error) {
		installs++
		return 0, nil
	}
	var stdout, stderr bytes.Buffer

	// Act
	exit := runWithRuntime([]string{"gate", "prepare"}, &stdout, &stderr, runtime)

	// Assert
	if exit != 2 || installs != 0 || !strings.Contains(stderr.String(), "cfo gate prepare:") {
		t.Fatalf("exit=%d installs=%d stdout=%s stderr=%s", exit, installs, &stdout, &stderr)
	}
}
