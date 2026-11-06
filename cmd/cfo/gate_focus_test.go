package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestGateFocusedRepairRecordsOnlyItsSelectedCommands(t *testing.T) {
	// Arrange
	dir := testStepModule(t, nil, map[string]string{"c/c.go": "package c\n\nconst Changed = true\n"})
	t.Chdir(dir)
	focus := filepath.Join(t.TempDir(), "focus.txt")
	if err := os.WriteFile(focus, []byte("test example.com/m/c --- FAIL: TestC (0.01s)\npath a/a.go\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NO_MISTAKES_TEST_FOCUS", focus)
	runtime := standIn()
	runtime.gateRun = func(command []string, _ string, _ []string, stdout, _ io.Writer) (int, error) {
		if command[1] == "test" {
			for _, target := range command {
				if strings.HasPrefix(target, "example.com/m/") {
					fmt.Fprintf(stdout, "{\"Action\":\"run\",\"Package\":%q,\"Test\":\"TestC\"}\n{\"Action\":\"pass\",\"Package\":%q,\"Test\":\"TestC\"}\n{\"Action\":\"pass\",\"Package\":%q}\n", target, target, target)
				}
			}
		}
		return 0, nil
	}

	// Act
	var stdout, stderr bytes.Buffer
	exit := gateTestWith(runtime, &stdout, &stderr, "--focused")

	// Assert
	if exit != 0 {
		t.Fatalf("exit=%d stdout=%s stderr=%s", exit, &stdout, &stderr)
	}
	report, _ := lastReport(t)
	want := [][]string{
		{"go", "vet", "example.com/m/a", "example.com/m/b"},
		{"go", "test", "-json", "-count=1", "-p", "2", "-timeout", "45m", "example.com/m/a", "example.com/m/b"},
		{"go", "test", "-json", "-count=1", "-p", "2", "-timeout", "45m", "-run", "^(TestC)$", "example.com/m/c"},
	}
	if len(report.Checks) != len(want) {
		t.Fatalf("checks=%+v; want %v", report.Checks, want)
	}
	for index, check := range report.Checks {
		if !slices.Equal(check.Command, want[index]) || check.Status != "passed" || check.Scope != "focused" {
			t.Errorf("check=%+v; want %v at focused scope", check, want[index])
		}
	}
	t.Logf("synthetic selected commands=%v; recorded duration_seconds=%.6f (fake check executor, real planner)", want, report.DurationSeconds)
}

func TestGateFocusedRepairRunsNamedFailureAndRepairDependents(t *testing.T) {
	// Arrange
	dir := testStepModule(t, nil, map[string]string{"c/c.go": "package c\n\nconst Changed = true\n"})
	t.Chdir(dir)
	files := map[string]string{
		"a/a.go":      "package a\n\nfunc A() int { return 2 }\n",
		"c/c_test.go": "package c\n\nimport \"testing\"\n\nfunc TestC(t *testing.T) {}\nfunc TestUnrelated(t *testing.T) { t.Fatal(\"not part of this repair\") }\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	focus := filepath.Join(t.TempDir(), "focus.txt")
	if err := os.WriteFile(focus, []byte("test example.com/m/c --- FAIL: TestC (0.01s)\npath a/a.go\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NO_MISTAKES_TEST_FOCUS", focus)

	// Act
	var stdout, stderr bytes.Buffer
	exit := gateTest(&stdout, &stderr, "--focused")

	// Assert
	if exit != 0 {
		t.Fatalf("exit=%d stdout=%s stderr=%s", exit, &stdout, &stderr)
	}
	report, _ := lastReport(t)
	var packages []string
	for _, check := range report.Checks {
		for _, result := range check.Packages {
			packages = append(packages, result.Package)
		}
	}
	if !slices.Equal(packages, []string{"example.com/m/a", "example.com/m/b", "example.com/m/c"}) {
		t.Fatalf("missing repair checks: %+v", report.Checks)
	}
}

func TestGateFocusedRepairRefusesToPassAnUnexecutedNamedFailure(t *testing.T) {
	// Arrange
	dir := testStepModule(t, nil, map[string]string{"a/a.go": "package a\n\nconst Changed = true\n"})
	t.Chdir(dir)
	focus := filepath.Join(t.TempDir(), "focus.txt")
	if err := os.WriteFile(focus, []byte("test example.com/m/a --- FAIL: TestRemoved (0.01s)\npath a/a.go\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NO_MISTAKES_TEST_FOCUS", focus)

	// Act
	var stdout, stderr bytes.Buffer
	exit := gateTestWith(standIn(), &stdout, &stderr, "--focused")

	// Assert
	if exit != 1 || !strings.Contains(stderr.String(), "TestRemoved") {
		t.Fatalf("unexecuted named failure passed: exit=%d stdout=%s stderr=%s", exit, &stdout, &stderr)
	}
}

func TestGateLintUsesTheAffectedVetPlanWithoutRunningTests(t *testing.T) {
	for _, test := range []struct {
		name   string
		change map[string]string
		want   []string
	}{
		{"known", map[string]string{"a/a.go": "package a\n\nconst Changed = true\n"}, []string{"go", "vet", "example.com/m/a", "example.com/m/b"}},
		{"unknown", map[string]string{"unknown.input": "changed\n"}, []string{"go", "vet", "./..."}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			dir := testStepModule(t, map[string]string{"config/verify.json": `{"version":1,"outside":[]}`}, test.change)
			t.Chdir(dir)
			runtime := standIn()
			var commands [][]string
			runtime.gateRun = func(command []string, _ string, _ []string, _, _ io.Writer) (int, error) {
				commands = append(commands, command)
				return 0, nil
			}

			// Act
			var stdout, stderr bytes.Buffer
			exit := runWithRuntime([]string{"gate", "lint"}, &stdout, &stderr, runtime)

			// Assert
			if exit != 0 || len(commands) != 1 || !slices.Equal(commands[0], test.want) {
				t.Fatalf("exit=%d commands=%v stdout=%s stderr=%s", exit, commands, &stdout, &stderr)
			}
			report, _ := lastReport(t)
			if report.Level != "lint" || len(report.Checks) != 1 || len(report.Checks[0].Packages) != 0 || strings.Contains(stdout.String(), "every package is tested") {
				t.Fatalf("lint claims test coverage: %+v stdout=%s", report, &stdout)
			}
			t.Logf("before=go vet ./...; after=%v; recorded duration_seconds=%.6f (fake check executor, real planner)", commands[0], report.DurationSeconds)
		})
	}
}

func TestGateFocusedRepairRejectsMissingOrRelativeFocusFiles(t *testing.T) {
	for _, filename := range []string{"", "relative.txt", filepath.Join(t.TempDir(), "missing.txt")} {
		t.Run(filename, func(t *testing.T) {
			// Arrange
			t.Setenv("NO_MISTAKES_TEST_FOCUS", filename)
			runtime := standIn()
			runtime.gateRun = func([]string, string, []string, io.Writer, io.Writer) (int, error) {
				t.Fatal("invalid focus ran a command")
				return 0, nil
			}

			// Act
			var stdout, stderr bytes.Buffer
			exit := gateTestWith(runtime, &stdout, &stderr, "--focused")

			// Assert
			if exit != 2 || stderr.Len() == 0 {
				t.Fatalf("exit=%d stdout=%s stderr=%s", exit, &stdout, &stderr)
			}
		})
	}
}
