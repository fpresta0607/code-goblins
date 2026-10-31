package main

import (
	"bytes"
	"slices"
	"strings"
	"testing"
)

func TestFastGateRejectsAnUnclassifiedCommandWithoutRunningTheSlowPackage(t *testing.T) {
	for _, test := range []struct {
		name, commands string
		wantExit       int
		failed         []string
	}{
		{"classified", `"help"`, 0, nil},
		{"unclassified", `"help", "new-command"`, 1, []string{"TestCommandsAreClassified"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			dir := testStepModule(t, map[string]string{
				"config/verify.json": `{"version":1,"slow_packages":["c"],"guards":[{"package":"c","tests":["TestCommandsAreClassified"],"why":"every command needs a classification"}]}`,
				"c/c.go":             "package c\nvar Commands = []string{}\n",
				"c/c_test.go": `package c
import "testing"
func TestCommandsAreClassified(t *testing.T) {
 for _, command := range Commands { if command != "help" { t.Fatalf("unclassified command: %s", command) } }
}
func TestSlowUnrelated(t *testing.T) { t.Fatal("unrelated slow test ran") }
`,
			}, map[string]string{"c/c.go": "package c\nvar Commands = []string{" + test.commands + "}\n"})
			t.Chdir(dir)

			// Act
			var stdout, stderr bytes.Buffer
			exit := gateTest(&stdout, &stderr)

			// Assert
			if exit != test.wantExit {
				t.Fatalf("exit=%d stdout=%s stderr=%s; required guard must reject publication", exit, &stdout, &stderr)
			}
			if test.wantExit == 1 && !strings.Contains(stdout.String(), "unclassified command: new-command") {
				t.Fatalf("guard did not report the unclassified command: %s", &stdout)
			}
			report, _ := lastReport(t)
			guardRuns := 0
			for _, check := range report.Checks {
				for _, result := range check.Packages {
					if result.Package == "example.com/m/c" {
						guardRuns++
						if result.Tests != 1 || !slices.Equal(result.Failed, test.failed) || len(result.Unfinished) != 0 {
							t.Fatalf("fast validation must run only the required guard: %+v", result)
						}
					}
				}
			}
			if guardRuns != 1 {
				t.Fatalf("required guard ran %d times; want once: %+v", guardRuns, report.Checks)
			}
		})
	}
}
