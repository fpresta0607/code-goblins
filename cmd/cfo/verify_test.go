package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	projectcfg "github.com/fpresta0607/code-goblins/internal/project"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/verify"
)

// verifyTask is a task of the project demo in a home of the test's own,
// whose record names fast as its fast tier.
func verifyTask(t *testing.T, fast string) commandRuntime {
	t.Helper()
	runtime, _ := measuredTask(t, `"verification": {"fast": [`+fast+`]}`)
	return runtime
}

// measuredTask is a task of the project demo, whose record holds settings,
// on a machine with memory to spare, with a verification store of the
// test's own. It returns the task's folder for what its tiers save.
func measuredTask(t *testing.T, settings string) (commandRuntime, string) {
	t.Helper()
	t.Setenv("CFO_VERIFY_DIR", t.TempDir())
	h := testHome(t)
	record := projectcfg.Path(h.Data, "demo")
	if err := os.MkdirAll(filepath.Dir(record), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(record, []byte(`{"project": "demo", `+settings+`}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(h.State, 0o755); err != nil {
		t.Fatal(err)
	}
	taskTmp := t.TempDir()
	if err := state.WriteTaskMeta(h.State, state.TaskMeta{ID: "t1", Project: filepath.Join(t.TempDir(), "demo"), Worktree: t.TempDir(), TaskTmp: taskTmp}); err != nil {
		t.Fatal(err)
	}
	runtime := testCommandRuntimeForHome(h)
	runtime.availableMemory = func() (supervisor.Memory, error) {
		return supervisor.Memory{Available: 16 << 30, CommitAvailable: 16 << 30}, nil
	}
	return runtime, taskTmp
}

// savedResults reads what a tier saved in path.
func savedResults(t *testing.T, path string) []verify.Result {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var results []verify.Result
	if err := json.Unmarshal(data, &results); err != nil {
		t.Fatal(err)
	}
	return results
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

// Each command of a tier runs as a measured execution, whose record the
// tier's result names.
func TestVerifyRecordsEachCommandAsAMeasuredExecution(t *testing.T) {
	// Arrange
	runtime, taskTmp := measuredTask(t, `"verification": {"fast": [["git", "version"]]}`)

	// Act
	var stdout, stderr bytes.Buffer
	exit := runWithRuntime([]string{"verify", "t1"}, &stdout, &stderr, runtime)

	// Assert
	if exit != 0 {
		t.Fatalf("exit = %d, stderr = %q", exit, stderr.String())
	}
	results := savedResults(t, filepath.Join(taskTmp, "verification-fast.json"))
	if len(results) != 1 || results[0].ExitCode != 0 || !strings.HasPrefix(results[0].Output, "git version") || filepath.Dir(results[0].Execution) != filepath.Join(os.Getenv("CFO_VERIFY_DIR"), "executions") {
		t.Fatalf("results = %+v, want git version's output and its execution record", results)
	}
	data, err := os.ReadFile(results[0].Execution)
	if err != nil {
		t.Fatal(err)
	}
	var execution verify.ExecutionRecord
	if err := json.Unmarshal(data, &execution); err != nil {
		t.Fatal(err)
	}
	if execution.Status != "passed" || !execution.Measurement.CleanupConfirmed || execution.Measurement.Usage.TotalProcesses == 0 {
		t.Fatalf("execution = %+v, want a measured pass", execution)
	}
}

// A tier that names no command, or an empty one, verified nothing, so it
// fails rather than passing.
func TestVerifyRefusesATierThatNamesNoCommand(t *testing.T) {
	for name, fast := range map[string]string{"no command": ``, "an empty command": `[]`} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			runtime := verifyTask(t, fast)

			// Act
			var stdout, stderr bytes.Buffer
			exit := runWithRuntime([]string{"verify", "t1"}, &stdout, &stderr, runtime)

			// Assert
			if exit != 1 || !strings.Contains(stderr.String(), "the fast tier names") {
				t.Fatalf("exit = %d, stderr = %q; want the tier refused", exit, stderr.String())
			}
		})
	}
}

// A tier whose result cannot be saved fails even when its commands passed,
// since nothing else would show what it did.
func TestVerifyFailsWhenItsResultCannotBeSaved(t *testing.T) {
	// Arrange
	runtime, taskTmp := measuredTask(t, `"verification": {"fast": [["git", "version"]]}`)
	if err := os.Mkdir(filepath.Join(taskTmp, "verification-fast.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(taskTmp, "verification-fast.json", "occupied"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	var stdout, stderr bytes.Buffer
	exit := runWithRuntime([]string{"verify", "t1"}, &stdout, &stderr, runtime)

	// Assert
	if exit != 1 || !strings.Contains(stderr.String(), "could not be saved") {
		t.Fatalf("exit = %d, stderr = %q; want an unsaved result to fail the tier", exit, stderr.String())
	}
}

// A tier's command waits in the machine's one line for heavy runs, and says
// so, while another run holds the turn.
func TestVerifyWaitsItsTurnBehindAnotherHeavyRun(t *testing.T) {
	// Arrange
	runtime, _ := measuredTask(t, `"verification": {"fast": [["git", "version"]]}`)
	store, err := verify.AdmissionDir()
	if err != nil {
		t.Fatal(err)
	}
	holder, err := verify.Admission{Dir: store, Available: func() (uint64, error) { return 16 << 30, nil }, Who: "another heavy run", Limit: time.Minute, Poll: 10 * time.Millisecond}.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	var stderr lockedBuffer
	exited := make(chan int, 1)

	// Act
	go func() { exited <- runWithRuntime([]string{"verify", "t1"}, &stdout, &stderr, runtime) }()
	for deadline := time.Now().Add(10 * time.Second); !strings.Contains(stderr.String(), "waiting for its turn") && time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
	}
	said := stderr.String()
	if err := holder.Release(); err != nil {
		t.Fatal(err)
	}
	var exit int
	select {
	case exit = <-exited:
	case <-time.After(20 * time.Second):
		t.Fatal("the tier did not run once the turn was free")
	}

	// Assert
	if !strings.Contains(said, "cfo verify: waiting for its turn") || !strings.Contains(said, "another heavy run") {
		t.Fatalf("stderr = %q, want the wait and who holds the turn", said)
	}
	if exit != 0 {
		t.Fatalf("exit = %d after the turn was free, stderr = %q", exit, stderr.String())
	}
}

// A security tier runs its commands the same way, each as a measured
// execution.
func TestSecurityRunsItsCommandsAsMeasuredExecutions(t *testing.T) {
	// Arrange
	runtime, taskTmp := measuredTask(t, `"security": {"mode": "always", "fast": [["git", "version"]]}`)

	// Act
	var stdout, stderr bytes.Buffer
	exit := runWithRuntime([]string{"security", "t1"}, &stdout, &stderr, runtime)

	// Assert
	if exit != 0 {
		t.Fatalf("exit = %d, stderr = %q", exit, stderr.String())
	}
	results := savedResults(t, filepath.Join(taskTmp, "security.json"))
	if len(results) != 1 || results[0].Execution == "" || results[0].Scope != "security" {
		t.Fatalf("results = %+v, want one measured security command", results)
	}
}
