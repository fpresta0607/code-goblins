package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gateRepo is a branch off main in a fresh repository, the shape a
// no-mistakes run worktree has when a repository gate runs its command.
func gateRepo(t *testing.T) (string, func(message string, content string)) {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	commit := func(message, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "guard_test.go"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		git("add", ".")
		git("commit", "-qm", message)
	}
	git("init", "-q", "--initial-branch=main")
	git("config", "user.email", "t@example.invalid")
	git("config", "user.name", "t")
	commit("base", "package x\n\nfunc TestGuardHolds(t *testing.T) {}\n")
	git("update-ref", "refs/remotes/origin/main", "HEAD")
	git("switch", "-qc", "feature")
	return dir, commit
}

func TestGateTestsKeptParksAGateThatDeletedATest(t *testing.T) {
	dir, commit := gateRepo(t)
	commit("no-mistakes(test): fix failing live test", "package x\n")
	t.Chdir(dir)

	var stdout, stderr bytes.Buffer
	exit := run([]string{"gate", "tests-kept"}, &stdout, &stderr)

	if exit != 1 {
		t.Fatalf("exit = %d, want 1 so the gate parks; stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	for _, want := range []string{"ask-user", "removed the test TestGuardHolds", "no-mistakes(test)"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout %q lacks %q", stdout.String(), want)
		}
	}
}

func TestGateTestsKeptPassesWhenTheGateKeptEveryTest(t *testing.T) {
	dir, commit := gateRepo(t)
	commit("no-mistakes(review): tighten the guard", "package x\n\nfunc TestGuardHolds(t *testing.T) { t.Log(1) }\n")
	t.Chdir(dir)

	var stdout, stderr bytes.Buffer
	exit := run([]string{"gate", "tests-kept"}, &stdout, &stderr)

	if exit != 0 || !strings.Contains(stdout.String(), "1 gate commit(s)") {
		t.Fatalf("exit=%d stdout=%q stderr=%q, want a pass that counts the one gate commit read", exit, stdout.String(), stderr.String())
	}
}

func TestGateTestsKeptRefusesWithoutABase(t *testing.T) {
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	t.Chdir(dir)

	var stdout, stderr bytes.Buffer
	if exit := run([]string{"gate", "tests-kept"}, &stdout, &stderr); exit != 2 {
		t.Fatalf("exit = %d, want 2 when no base can be found; stderr=%s", exit, stderr.String())
	}
}
