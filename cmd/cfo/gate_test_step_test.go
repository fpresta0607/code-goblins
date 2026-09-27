package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// testStepModule is a branch off main in a repository holding a Go module:
// a leaf package a, a package b whose test imports a and fails when it sees
// the fleet's CFO_HOME, and a package c nothing imports whose test fails.
// The branch then changes what change names.
func testStepModule(t *testing.T, change map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	write := func(files map[string]string) {
		for name, content := range files {
			path := filepath.Join(dir, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write(map[string]string{
		"go.mod":      "module example.com/m\n\ngo 1.22\n",
		"a/a.go":      "package a\n\nfunc A() int { return 1 }\n",
		"b/b.go":      "package b\n",
		"b/b_test.go": "package b\n\nimport (\n\t\"os\"\n\t\"testing\"\n\n\t\"example.com/m/a\"\n)\n\nfunc TestB(t *testing.T) {\n\tif os.Getenv(\"CFO_HOME\") != \"\" || a.A() == 0 {\n\t\tt.Fatal(\"the fleet's CFO_HOME reached the test\")\n\t}\n}\n",
		"c/c.go":      "package c\n",
		"c/c_test.go": "package c\n\nimport \"testing\"\n\nfunc TestC(t *testing.T) { t.Fatal(\"c is never chosen\") }\n",
		"README.md":   "module\n",
	})
	git("init", "-q", "--initial-branch=main")
	git("config", "user.email", "t@example.invalid")
	git("config", "user.name", "t")
	git("add", ".")
	git("commit", "-qm", "base")
	git("update-ref", "refs/remotes/origin/main", "HEAD")
	git("switch", "-qc", "feature")
	write(change)
	git("add", ".")
	git("commit", "-qm", "change")
	return dir
}

// The step names each package it chose and why, tests only those (c, whose
// test always fails, is never run), and runs them without the fleet's home
// even when the gate step inherited it.
func TestGateTestRunsTheChangedPackagesAndTheirImportersWithoutTheFleetHome(t *testing.T) {
	// Arrange
	dir := testStepModule(t, map[string]string{"a/a.go": "package a\n\nfunc A() int { return 2 }\n"})
	t.Chdir(dir)
	t.Setenv("CFO_HOME", filepath.Join(dir, "fleet"))

	// Act
	var stdout, stderr bytes.Buffer
	exit := run([]string{"gate", "test"}, &stdout, &stderr)

	// Assert
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	for _, want := range []string{"- example.com/m/a (changed)", "- example.com/m/b (imports example.com/m/a)", "CI runs every package"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout %q lacks %q", stdout.String(), want)
		}
	}
	if strings.Contains(stdout.String(), "example.com/m/c") {
		t.Errorf("stdout %q names a package the branch neither changed nor feeds", stdout.String())
	}
}

// A branch that changed no Go package says so in one line and passes.
func TestGateTestSaysSoWhenNoPackageChanged(t *testing.T) {
	// Arrange
	dir := testStepModule(t, map[string]string{"README.md": "more\n"})
	t.Chdir(dir)

	// Act
	var stdout, stderr bytes.Buffer
	exit := run([]string{"gate", "test"}, &stdout, &stderr)

	// Assert
	if exit != 0 || !strings.Contains(stdout.String(), "no Go package changed") {
		t.Fatalf("exit=%d stdout=%q stderr=%q, want a clear line and exit 0", exit, stdout.String(), stderr.String())
	}
}

// A failing test in a chosen package fails the step, which is what parks or
// fixes the gate's test step.
func TestGateTestFailsWhenAChosenPackageFails(t *testing.T) {
	// Arrange
	dir := testStepModule(t, map[string]string{"c/c.go": "package c\n\n// changed\n"})
	t.Chdir(dir)

	// Act
	var stdout, stderr bytes.Buffer
	exit := run([]string{"gate", "test"}, &stdout, &stderr)

	// Assert
	if exit != 1 || !strings.Contains(stdout.String(), "- example.com/m/c (changed)") {
		t.Fatalf("exit=%d stdout=%q, want c chosen and the step failed", exit, stdout.String())
	}
}
