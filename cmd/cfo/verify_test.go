package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	projectcfg "github.com/fpresta0607/code-goblins/internal/project"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// verifyTask is a task of the project demo in a home of the test's own,
// whose record names fast as its fast tier.
func verifyTask(t *testing.T, fast string) commandRuntime {
	t.Helper()
	h := testHome(t)
	record := projectcfg.Path(h.Data, "demo")
	if err := os.MkdirAll(filepath.Dir(record), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(record, []byte(`{"project": "demo", "verification": {"fast": [`+fast+`]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(h.State, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteTaskMeta(h.State, state.TaskMeta{ID: "t1", Project: filepath.Join(t.TempDir(), "demo"), Worktree: t.TempDir(), TaskTmp: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	return testCommandRuntimeForHome(h)
}

// A tier that fails says what its failed check said last and where the rest
// is kept. A check ends with the line that names what failed, so whoever ran
// the tier, a goblin before its push or the CFO, reads it without opening a
// file.
func TestVerifySaysWhatTheFailedCheckSaidLast(t *testing.T) {
	// Arrange
	runtime := verifyTask(t, `["git", "-c", "alias.check=!echo picked 2 checks && echo failed at check 2 of 2: TestA failed && exit 1", "check"]`)

	// Act
	var stdout, stderr bytes.Buffer
	exit := runWithRuntime([]string{"verify", "t1"}, &stdout, &stderr, runtime)

	// Assert
	if exit != 1 {
		t.Fatalf("exit = %d, want 1; stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	lines := strings.Split(strings.TrimSpace(stderr.String()), "\n")
	if len(lines) != 3 || strings.TrimSpace(lines[0]) != "failed at check 2 of 2: TestA failed" || !strings.HasPrefix(lines[1], "verify git exited 1") || !strings.HasSuffix(strings.TrimSpace(lines[2]), "verification-fast.json") {
		t.Errorf("stderr = %q, want the check's last line, then that the tier failed, then the file that holds what it said", stderr.String())
	}
}

// A tier that passes prints the file that holds what its checks said, and
// nothing else.
func TestVerifyPrintsOnlyItsRecordWhenTheTierPasses(t *testing.T) {
	// Arrange
	runtime := verifyTask(t, `["git", "version"]`)

	// Act
	var stdout, stderr bytes.Buffer
	exit := runWithRuntime([]string{"verify", "t1"}, &stdout, &stderr, runtime)

	// Assert
	if exit != 0 || stderr.Len() != 0 || !strings.HasSuffix(strings.TrimSpace(stdout.String()), "verification-fast.json") {
		t.Errorf("exit = %d, stdout = %q, stderr = %q; want 0 and the record's path alone", exit, stdout.String(), stderr.String())
	}
}
