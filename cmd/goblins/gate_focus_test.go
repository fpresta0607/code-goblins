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

	"github.com/fpresta0607/code-goblins/internal/verify"
)

func TestGateFocusedRepairRecordsOnlyItsSelectedCommands(t *testing.T) {
	// Arrange
	dir := testStepModule(t, nil, map[string]string{"c/c.go": "package c\n\nconst Changed = true\n"})
	t.Chdir(dir)
	focus := filepath.Join(t.TempDir(), "focus.txt")
	if err := os.WriteFile(focus, []byte("test example.com/m/a --- FAIL: TestC (0.01s)\ntest example.com/m/c --- FAIL: TestC (0.01s)\npath a/a.go\n"), 0o600); err != nil {
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

// A repair round that cannot narrow, here a review repair that names a path
// and no failed test, runs the step's configured level, never every package,
// says so in one line, and its report carries that level (#2176). On a project
// whose hosted CI is its test evidence that level is changed, which still
// runs, by name and once, the test the repair added, even when that test is
// also the failed test the repair names.
func TestGateFocusedRepairThatCannotNarrowRunsAndReportsTheConfiguredLevel(t *testing.T) {
	hostedChange := map[string]string{
		"b/regression_test.go": "package b\n\nimport \"testing\"\n\nfunc TestRegression(t *testing.T) {}\n",
		"c/c_test.go":          "package c\n\nimport \"testing\"\n\nfunc TestC(t *testing.T) {}\n",
	}
	regression := []string{"go", "test", "-json", "-count=1", "-p", "2", "-timeout", "45m", "-run", "^(TestRegression)$", "example.com/m/b"}
	for _, test := range []struct {
		name     string
		evidence string
		change   map[string]string
		focus    string
		level    string
		why      string
		named    []string
	}{
		{name: "fast", change: map[string]string{"a/a.go": "package a\n\nfunc A() int { return 2 }\n"}, focus: "path a/a.go\n", level: "fast", why: "the repair named no failed test"},
		{name: "hosted CI", evidence: "hosted-ci", change: hostedChange, focus: "path b/regression_test.go\n", level: "changed", why: "the repair named no failed test", named: regression},
		{
			name:     "hosted CI with the failed test it edited",
			evidence: "hosted-ci",
			change:   hostedChange,
			focus:    "test example.com/m/b --- FAIL: TestRegression (0.01s)\npath b/regression_test.go\npath strange.input\n",
			level:    "changed",
			why:      "a repair path has no mapped check or outside classification",
			named:    regression,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			t.Setenv("GOBLINS_TEST_EVIDENCE", test.evidence)
			dir := testStepModule(t, nil, test.change)
			t.Chdir(dir)
			focus := filepath.Join(t.TempDir(), "focus.txt")
			if err := os.WriteFile(focus, []byte(test.focus), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("NO_MISTAKES_TEST_FOCUS", focus)
			runtime := standIn()
			var commands [][]string
			runtime.gateRun = func(command []string, _ string, _ []string, stdout, _ io.Writer) (int, error) {
				commands = append(commands, command)
				if command[1] != "test" {
					return 0, nil
				}
				for _, target := range command {
					if !strings.HasPrefix(target, "example.com/m/") {
						continue
					}
					if run := slices.Index(command, "-run"); run >= 0 {
						name := strings.Trim(command[run+1], "^()$")
						fmt.Fprintf(stdout, "{\"Action\":\"run\",\"Package\":%q,\"Test\":%q}\n{\"Action\":\"pass\",\"Package\":%q,\"Test\":%q}\n", target, name, target, name)
					}
					fmt.Fprintf(stdout, "{\"Action\":\"pass\",\"Package\":%q}\n", target)
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
			if report.Level != test.level {
				t.Errorf("report level = %q; want the configured level %q", report.Level, test.level)
			}
			if want := "focused check runs the configured level " + test.level + " over the repaired paths, not the full level, since " + test.why; !strings.Contains(stdout.String(), want) {
				t.Errorf("stdout = %s; want the line %q", &stdout, want)
			}
			for _, command := range commands {
				if slices.Contains(command, "./...") || slices.Contains(command, "example.com/m/c") {
					t.Errorf("ran %v; want neither every package nor the unrepaired package c", command)
				}
			}
			runs := 0
			for _, command := range commands {
				if slices.Equal(command, test.named) {
					runs++
				}
			}
			if test.named != nil && runs != 1 {
				t.Errorf("commands = %v; want the repaired test run by name once, %v", commands, test.named)
			}
		})
	}
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

func TestGateFocusedRepairRequiresEveryNamedFailureAcrossTheRun(t *testing.T) {
	for _, scenario := range []struct {
		name          string
		failure       string
		repair        string
		alone         string
		packageChange string
	}{
		{name: "affected", failure: "a", repair: "a/a.go"},
		{name: "named", failure: "c", repair: "a/a.go"},
		{name: "alone", failure: "c", repair: "c/c.go", alone: "c"},
		{name: "full", failure: "c", repair: "go.mod"},
		{name: "full split regular", failure: "a", repair: "go.mod", alone: "c"},
		{name: "full split alone", failure: "c", repair: "go.mod", alone: "c"},
		{name: "removed", failure: "c", repair: "a/a.go", packageChange: "removed"},
		{name: "renamed split", failure: "c", repair: "a/a.go", alone: "b", packageChange: "renamed"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			base := map[string]string{}
			if scenario.alone != "" {
				base["config/verify.json"] = fmt.Sprintf(`{"version":1,"alone_packages":[%q]}`, scenario.alone)
			}
			dir := testStepModule(t, base, map[string]string{"a/a.go": "package a\n\nconst Changed = true\n"})
			t.Chdir(dir)
			packages := []string{"example.com/m/a", "example.com/m/b", "example.com/m/c"}
			switch scenario.packageChange {
			case "removed":
				for _, name := range []string{"c/c.go", "c/c_test.go"} {
					if err := os.Remove(filepath.Join(dir, filepath.FromSlash(name))); err != nil {
						t.Fatal(err)
					}
				}
				packages = packages[:2]
			case "renamed":
				if err := os.Rename(filepath.Join(dir, "c"), filepath.Join(dir, "renamed")); err != nil {
					t.Fatal(err)
				}
				packages[2] = "example.com/m/renamed"
			}
			focus := filepath.Join(t.TempDir(), "focus.txt")
			failedPackage := "example.com/m/" + scenario.failure
			text := fmt.Sprintf("test %s --- FAIL: TestBroken (0.01s)\npath %s\n", failedPackage, scenario.repair)
			if err := os.WriteFile(focus, []byte(text), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("NO_MISTAKES_TEST_FOCUS", focus)
			outcomes := []string{"pass"}
			if scenario.name == "named" {
				outcomes = []string{"missing", "renamed", "skip", "unfinished", "pass"}
			}
			for _, outcome := range outcomes {
				t.Run(outcome, func(t *testing.T) {
					t.Setenv("GOBLINS_VERIFY_DIR", t.TempDir())
					runtime := standIn()
					runtime.gateRun = func(command []string, _ string, _ []string, stdout, _ io.Writer) (int, error) {
						if command[1] != "test" {
							return 0, nil
						}
						for _, target := range packages {
							if !slices.Contains(command, target) && !slices.Contains(command, "./...") {
								continue
							}
							name, action := "TestBroken", "pass"
							if target == failedPackage {
								switch outcome {
								case "missing":
									continue
								case "renamed":
									name = "TestRenamed"
								case "skip", "unfinished":
									action = outcome
								}
							}
							fmt.Fprintf(stdout, "{\"Action\":\"run\",\"Package\":%q,\"Test\":%q}\n", target, name)
							if action != "unfinished" {
								fmt.Fprintf(stdout, "{\"Action\":%q,\"Package\":%q,\"Test\":%q}\n", action, target, name)
							}
							fmt.Fprintf(stdout, "{\"Action\":\"pass\",\"Package\":%q}\n", target)
						}
						return 0, nil
					}

					var stdout, stderr bytes.Buffer
					exit := gateTestWith(runtime, &stdout, &stderr, "--focused")

					wantExit, wantStatus := 1, "failed"
					if outcome == "pass" && scenario.packageChange == "" {
						wantExit, wantStatus = 0, "passed"
					}
					report, _ := lastReport(t)
					if exit != wantExit || report.Status != wantStatus || wantExit == 1 && !strings.Contains(stderr.String(), failedPackage+" TestBroken was missing, skipped or unfinished") {
						t.Fatalf("exit=%d report=%+v stdout=%s stderr=%s", exit, report, &stdout, &stderr)
					}
				})
			}
		})
	}
}

func TestGateLintUsesTheAffectedVetPlanWithoutRunningTests(t *testing.T) {
	for _, test := range []struct {
		name   string
		change map[string]string
		want   []string
		exit   int
	}{
		{"known", map[string]string{"a/a.go": "package a\n\nconst Changed = true\n"}, []string{"go", "vet", "example.com/m/a", "example.com/m/b"}, 0},
		{"unknown", map[string]string{"unknown.input": "changed\n"}, []string{"go", "vet", "./..."}, 0},
		{"failed", map[string]string{"a/a.go": "package a\n\nconst Changed = true\n"}, []string{"go", "vet", "example.com/m/a", "example.com/m/b"}, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			dir := testStepModule(t, map[string]string{"config/verify.json": `{"version":1,"outside":[]}`}, test.change)
			t.Chdir(dir)
			reportPath := filepath.Join(os.Getenv("GOBLINS_VERIFY_DIR"), "reports", "m", "existing.json")
			if err := verify.Save(reportPath, verify.Report{Version: verify.ReportVersion, Project: "m", Level: "fast", Status: "passed"}); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(reportPath)
			if err != nil {
				t.Fatal(err)
			}
			storedFiles, err := filepath.Glob(filepath.Join(os.Getenv("GOBLINS_VERIFY_DIR"), "reports", "*", "*"))
			if err != nil {
				t.Fatal(err)
			}
			runtime := standIn()
			var commands [][]string
			runtime.gateRun = func(command []string, _ string, _ []string, _, _ io.Writer) (int, error) {
				commands = append(commands, command)
				return test.exit, nil
			}

			// Act
			var stdout, stderr bytes.Buffer
			exit := runWithRuntime([]string{"gate", "lint"}, &stdout, &stderr, runtime)

			// Assert
			if exit != test.exit || len(commands) != 1 || !slices.Equal(commands[0], test.want) {
				t.Fatalf("exit=%d commands=%v stdout=%s stderr=%s", exit, commands, &stdout, &stderr)
			}
			reports, err := verify.Reports("m")
			if err != nil || len(reports) != 1 || reports[0].Level != "fast" || strings.Contains(stdout.String(), "every package is tested") {
				t.Fatalf("lint replaced test evidence: %+v, %v stdout=%s", reports, err, &stdout)
			}
			after, err := os.ReadFile(reportPath)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("lint changed the test report: %v", err)
			}
			afterFiles, err := filepath.Glob(filepath.Join(os.Getenv("GOBLINS_VERIFY_DIR"), "reports", "*", "*"))
			if err != nil || !slices.Equal(storedFiles, afterFiles) {
				t.Fatalf("lint added test evidence: files=%v, %v", afterFiles, err)
			}
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
