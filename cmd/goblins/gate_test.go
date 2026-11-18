package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// gateRepo is a branch off main in a fresh repository, the shape a
// no-mistakes run worktree has when a repository gate runs its command. The
// gate's database is an empty scratch home, never the machine's own.
func gateRepo(t *testing.T) (string, func(message string, content string)) {
	t.Helper()
	t.Setenv("NM_HOME", t.TempDir())
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

// Issue 1959: tests-kept gave its whole check one deadline, so on a loaded
// machine a branch whose git calls together outlasted it failed the check,
// though no one call was stuck. Each call gets the limit, so git calls held
// 3 s each, two together outlasting a 5 s limit, finish the check.
func TestGateTestsKeptFinishesWhenItsGitCallsTogetherOutlastOneCallsLimit(t *testing.T) {
	// Arrange
	dir, commit := gateRepo(t)
	commit("no-mistakes(review): tighten the guard", "package x\n\nfunc TestGuardHolds(t *testing.T) { t.Log(1) }\n")
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	slow, held := t.TempDir(), t.TempDir()
	first, second := filepath.Join(held, "1"), filepath.Join(held, "2")
	for name, script := range map[string]string{
		"git.cmd": `@if not exist "` + first + `" (type nul > "` + first + `" & ping -n 4 127.0.0.1 >nul) else if not exist "` + second + `" (type nul > "` + second + `" & ping -n 4 127.0.0.1 >nul)` + "\r\n" +
			`@"` + realGit + `" %*` + "\r\n",
		"git": "#!/bin/sh\n" +
			"if [ ! -e '" + first + "' ]; then : > '" + first + "'; sleep 3\n" +
			"elif [ ! -e '" + second + "' ]; then : > '" + second + "'; sleep 3\n" +
			"fi\n" +
			"exec '" + realGit + "' \"$@\"\n",
	} {
		if err := os.WriteFile(filepath.Join(slow, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", slow+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Chdir(dir)
	runtime := defaultCommandRuntime()
	runtime.testsKeptCallLimit = 5 * time.Second

	// Act
	var stdout, stderr bytes.Buffer
	exit := runWithRuntime([]string{"gate", "tests-kept"}, &stdout, &stderr, runtime)

	// Assert
	if _, err := os.Stat(second); err != nil {
		t.Fatalf("premise: the stand-in held fewer than two git calls (%v), so they did not together outlast the limit", err)
	}
	if exit != 0 || !strings.Contains(stdout.String(), "tests kept: 1 gate commit(s) since") || !strings.Contains(stdout.String(), "checked, and none deleted or skipped a test") {
		t.Fatalf("exit=%d stdout=%q stderr=%q, want the check to finish and count the one gate commit read", exit, stdout.String(), stderr.String())
	}
}

func TestGateTestsKeptRefusesWithoutABase(t *testing.T) {
	t.Setenv("NM_HOME", t.TempDir())
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

func gitHead(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// The first line names the HEAD the check read, on a pass and on a park: it
// is what the park's stored summary keeps, and so what a later run reads to
// know which head a person let through.
func TestGateTestsKeptNamesTheHeadItCheckedFirst(t *testing.T) {
	for _, test := range []struct {
		name    string
		message string
		content string
		exit    int
	}{
		{name: "a pass", message: "no-mistakes(review): tighten the guard", content: "package x\n\nfunc TestGuardHolds(t *testing.T) { t.Log(1) }\n", exit: 0},
		{name: "a park", message: "no-mistakes(test): fix failing live test", content: "package x\n", exit: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			dir, commit := gateRepo(t)
			commit(test.message, test.content)
			t.Chdir(dir)

			// Act
			var stdout, stderr bytes.Buffer
			exit := run([]string{"gate", "tests-kept"}, &stdout, &stderr)

			// Assert
			first, _, _ := strings.Cut(stdout.String(), "\n")
			if exit != test.exit || first != "tests kept: checked HEAD "+gitHead(t, dir) {
				t.Fatalf("exit=%d first line %q, want exit %d and the checked HEAD; stderr=%s", exit, first, test.exit, stderr.String())
			}
		})
	}
}

// Run 1 parks on a gate deletion and a person approves it, which no-mistakes
// records as a user_declined round of a completed gate step whose summary is
// the check's own output. The goblin fixes CI in a commit of its own and run
// 2 does not park on the approved deletion again.
func TestGateTestsKeptLeavesOutWhatAPersonApprovedAtAnEarlierPark(t *testing.T) {
	sqlite, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	// Arrange
	dir, commit := gateRepo(t)
	commit("no-mistakes(test): fix failing live test", "package x\n")
	t.Chdir(dir)
	var parked, stderr bytes.Buffer
	if exit := run([]string{"gate", "tests-kept"}, &parked, &stderr); exit != 1 {
		t.Fatalf("premise: run 1 parks, exit=%d stdout=%s stderr=%s", exit, parked.String(), stderr.String())
	}
	findings, err := json.Marshal(map[string]any{"findings": []any{}, "summary": parked.String()})
	if err != nil {
		t.Fatal(err)
	}
	sql := `CREATE TABLE step_results(id TEXT,run_id TEXT,step_name TEXT,status TEXT);
CREATE TABLE step_rounds(id TEXT,step_result_id TEXT,round INTEGER,selection_source TEXT,findings_json TEXT,created_at INTEGER);
INSERT INTO step_results VALUES('step','run','gate.lint.tests-kept','completed');
INSERT INTO step_rounds VALUES('round','step',1,'user_declined','` + strings.ReplaceAll(string(findings), "'", "''") + `',1);`
	if out, err := exec.Command(sqlite, filepath.Join(os.Getenv("NM_HOME"), "state.sqlite"), sql).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %s %v", out, err)
	}
	commit("fix: the CI failure", "package x\n\n// the CI fix\n")

	// Act
	var stdout bytes.Buffer
	exit := run([]string{"gate", "tests-kept"}, &stdout, &stderr)

	// Assert
	if exit != 0 || !strings.Contains(stdout.String(), "left out 1 gate commit(s)") {
		t.Fatalf("exit=%d stdout=%q stderr=%q, want a pass that says it left the approved commit out", exit, stdout.String(), stderr.String())
	}
}

// With no readable database the check reads every gate commit, as it did
// before it read approvals, and says so.
func TestGateTestsKeptReadsEveryGateCommitWhenApprovalsCannotBeRead(t *testing.T) {
	// Arrange
	dir, commit := gateRepo(t)
	commit("no-mistakes(test): fix failing live test", "package x\n")
	t.Chdir(dir)

	// Act
	var stdout, stderr bytes.Buffer
	exit := run([]string{"gate", "tests-kept"}, &stdout, &stderr)

	// Assert
	if exit != 1 || !strings.Contains(stdout.String(), "approvals given at earlier parks were not read") || !strings.Contains(stdout.String(), "removed the test TestGuardHolds") {
		t.Fatalf("exit=%d stdout=%q, want a park that says approvals were not read", exit, stdout.String())
	}
}
