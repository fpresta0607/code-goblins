package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

func TestGatePrepareConfiguredCommandReadiesFrontendBeforeTest(t *testing.T) {
	wantCommand := []string{"npm", "ci"}
	if runtime.GOOS == "windows" {
		wantCommand = []string{"cmd.exe", "/d", "/c", "npm", "ci"}
	}
	// Arrange
	data, err := os.ReadFile(filepath.Join(repoRootFromCmdCEO(t), ".no-mistakes.yaml"))
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
		if !slices.Equal(command, wantCommand) || fsx.LongPath(where) != fsx.LongPath(filepath.Join(dir, "frontend")) {
			t.Fatalf("installer = %q in %s", command, where)
		}
		return 0, os.MkdirAll(filepath.Join(where, "node_modules"), 0o755)
	}
	var stdout, stderr bytes.Buffer

	// Act: consume the configured native preparation command, before Test.
	command := strings.Fields(config.Commands.Prepare)
	if len(command) < 2 || command[0] != "goblins" {
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
	wantCommand := []string{"npm", "ci"}
	if runtime.GOOS == "windows" {
		wantCommand = []string{"cmd.exe", "/d", "/c", "npm", "ci"}
	}
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
				if !slices.Equal(command, wantCommand) || fsx.LongPath(where) != fsx.LongPath(filepath.Join(dir, "frontend")) {
					t.Fatalf("installer = %q in %s", command, where)
				}
				for _, value := range env {
					if strings.HasPrefix(value, "GOBLINS_HOME=") || strings.HasPrefix(value, "GOBLINS_STATE_OVERRIDE=") {
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

// A gate's preparation installs only once the machine has the memory, as the
// test after it does, never under the floor (#2367): a locked install under the
// floor failed an approved run's Test step before any test ran.
func TestGatePrepareWaitsForTheMemoryFloorBeforeItInstalls(t *testing.T) {
	// Arrange
	dir := testStepModule(t, map[string]string{
		".gitignore":                 "frontend/node_modules/\n",
		"frontend/package.json":      `{"name":"board","version":"1.0.0"}`,
		"frontend/package-lock.json": `{"name":"board","lockfileVersion":3,"packages":{}}`,
	}, map[string]string{"frontend/board.ts": "changed\n"})
	t.Chdir(dir)
	var readings atomic.Int32
	runtime := standIn()
	runtime.availableMemory = func() (supervisor.Memory, error) {
		if readings.Add(1) <= 2 {
			return supervisor.Memory{Available: 1 << 30, CommitAvailable: 16 << 30}, nil
		}
		return supervisor.Memory{Available: 16 << 30, CommitAvailable: 16 << 30}, nil
	}
	var stdout, stderr bytes.Buffer
	saidBeforeInstall := ""
	runtime.gateRun = func([]string, string, []string, io.Writer, io.Writer) (int, error) {
		saidBeforeInstall = stdout.String()
		return 0, nil
	}

	// Act
	exit := runWithRuntime([]string{"gate", "prepare"}, &stdout, &stderr, runtime)

	// Assert
	waited := "goblins gate prepare: waiting for its turn (0s so far): 1.0 GB of memory is available and the floor is 4.0 GB"
	took := "goblins gate prepare: took its turn after "
	if exit != 0 || !strings.Contains(saidBeforeInstall, waited) || !strings.Contains(saidBeforeInstall, took) {
		t.Fatalf("exit=%d; said before the install %q, want %q and then %q; stderr=%s", exit, saidBeforeInstall, waited, took, &stderr)
	}
}

func TestGatePreparePropagatesLockedInstallerFailures(t *testing.T) {
	wantCommand := []string{"npm", "ci"}
	if runtime.GOOS == "windows" {
		wantCommand = []string{"cmd.exe", "/d", "/c", "npm", "ci"}
	}
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
				if !slices.Equal(command, wantCommand) {
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

func TestGatePrepareRunsWindowsNpmShim(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows npm.cmd launch regression")
	}
	for _, test := range []struct {
		name          string
		installerExit string
		wantExit      int
	}{
		{"success", "0", 0},
		{"installer failure", "7", 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := testStepModule(t, map[string]string{
				".gitignore":                 "frontend/node_modules/\n",
				"frontend/package.json":      `{"name":"board","version":"1.0.0"}`,
				"frontend/package-lock.json": `{"name":"board","lockfileVersion":3,"packages":{}}`,
			}, map[string]string{"frontend/board.ts": "changed\n"})
			t.Chdir(dir)
			installerDir := filepath.Join(t.TempDir(), "npm shim")
			if err := os.MkdirAll(installerDir, 0o755); err != nil {
				t.Fatal(err)
			}
			installer := "@echo off\r\nif not \"%*\"==\"ci\" exit /b 91\r\nif not exist package-lock.json exit /b 92\r\nif defined GOBLINS_HOME exit /b 93\r\nif defined GOBLINS_STATE_OVERRIDE exit /b 94\r\nmkdir node_modules\r\necho locked installer ran\r\nexit /b " + test.installerExit + "\r\n"
			if err := os.WriteFile(filepath.Join(installerDir, "npm.cmd"), []byte(installer), 0o644); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", installerDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("GOBLINS_HOME", filepath.Join(dir, "fleet"))
			t.Setenv("GOBLINS_STATE_OVERRIDE", filepath.Join(dir, "fleet", "state"))
			var stdout, stderr bytes.Buffer

			exit := runWithRuntime([]string{"gate", "prepare"}, &stdout, &stderr, defaultCommandRuntime())

			if exit != test.wantExit || !strings.Contains(stdout.String(), "locked installer ran") {
				t.Fatalf("exit=%d, want %d; stdout=%s stderr=%s", exit, test.wantExit, &stdout, &stderr)
			}
			if _, err := os.Stat(filepath.Join(dir, "frontend", "node_modules")); err != nil {
				t.Fatalf("own dependencies not prepared: %v", err)
			}
			if test.wantExit != 0 && !strings.Contains(stderr.String(), "exit 7") {
				t.Fatalf("installer failure not propagated: %s", &stderr)
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
	if exit != 2 || installs != 0 || !strings.Contains(stderr.String(), "goblins gate prepare:") {
		t.Fatalf("exit=%d installs=%d stdout=%s stderr=%s", exit, installs, &stdout, &stderr)
	}
}
