package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestFastGateRejectsAnUnclassifiedCommandWithoutRunningTheSlowPackage(t *testing.T) {
	// Arrange
	dir := testStepModule(t, map[string]string{
		"config/verify.json": `{"version":1,"slow_packages":["c"],"guards":[{"package":"c","tests":["TestCommandsAreClassified"],"why":"every command needs a classification"}]}`,
		"c/c.go":             "package c\nvar Commands = []string{\"help\"}\n",
		"c/c_test.go": `package c
import "testing"
func TestCommandsAreClassified(t *testing.T) {
 for _, command := range Commands { if command != "help" { t.Fatalf("unclassified command: %s", command) } }
}
func TestSlowUnrelated(t *testing.T) { t.Fatal("unrelated slow test ran") }
`,
	}, map[string]string{"c/c.go": "package c\nvar Commands = []string{\"help\", \"new-command\"}\n"})
	t.Chdir(dir)

	// Act
	var stdout, stderr bytes.Buffer
	exit := gateTest(&stdout, &stderr)

	// Assert
	if exit != 1 || !strings.Contains(stdout.String()+stderr.String(), "TestCommandsAreClassified") {
		t.Fatalf("exit=%d stdout=%s stderr=%s; required guard must reject publication", exit, &stdout, &stderr)
	}
	report, _ := lastReport(t)
	for _, check := range report.Checks {
		if strings.Contains(check.Output, "unrelated slow test ran") {
			t.Fatal("fast validation ran the unrelated slow test")
		}
	}
}
