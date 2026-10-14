package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/proc"
	projectcfg "github.com/fpresta0607/code-goblins/internal/project"
	"github.com/fpresta0607/code-goblins/internal/standin"
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
// test's own and a worktree with one commit. It returns the task's folder
// for what its tiers save.
func measuredTask(t *testing.T, settings string) (commandRuntime, string) {
	t.Helper()
	worktree := t.TempDir()
	gitIn(t, worktree, "init", "-q")
	if err := os.WriteFile(filepath.Join(worktree, "a.txt"), []byte("a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitIn(t, worktree, "add", "a.txt")
	gitIn(t, worktree, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "-m", "start")
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
	if err := state.WriteTaskMeta(h.State, state.TaskMeta{ID: "t1", Project: filepath.Join(t.TempDir(), "demo"), Worktree: worktree, TaskTmp: taskTmp}); err != nil {
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

// Each command's result keeps the source it ran on, read once its turn was
// taken and once it had ended.
func TestVerifyRecordsTheSourceEachCommandRanOn(t *testing.T) {
	// Arrange
	runtime, taskTmp := measuredTask(t, `"verification": {"fast": [["git", "version"]]}`)
	h, err := runtime.resolveHome()
	if err != nil {
		t.Fatal(err)
	}
	meta, err := state.ReadTaskMeta(h.State, "t1")
	if err != nil {
		t.Fatal(err)
	}

	// Act
	var stdout, stderr bytes.Buffer
	exit := runWithRuntime([]string{"verify", "t1"}, &stdout, &stderr, runtime)

	// Assert
	results := savedResults(t, filepath.Join(taskTmp, "verification-fast.json"))
	tree := gitIn(t, meta.Worktree, "rev-parse", "HEAD^{tree}")
	want := verify.Source{Commit: gitIn(t, meta.Worktree, "rev-parse", "HEAD"), CommitTree: tree, Tree: tree, IsComplete: true}
	if exit != 0 || stderr.Len() != 0 || len(results) != 1 {
		t.Fatalf("exit = %d, stderr = %q, results = %+v; want one quiet pass", exit, stderr.String(), results)
	}
	if tested, err := results[0].Source.Tested(); err != nil || tested != want {
		t.Errorf("the command tested %+v (%v); want %+v", tested, err, want)
	}
}

// A command whose source changed while it ran keeps its own result, and the
// tier says that it tested no one source.
func TestVerifySaysWhenACommandsSourceChangedWhileItRan(t *testing.T) {
	// Arrange
	runtime, taskTmp := measuredTask(t, `"verification": {"fast": [["cmd", "/d", "/c", "echo changed> moved.txt"]]}`)

	// Act
	var stdout, stderr bytes.Buffer
	exit := runWithRuntime([]string{"verify", "t1"}, &stdout, &stderr, runtime)

	// Assert
	results := savedResults(t, filepath.Join(taskTmp, "verification-fast.json"))
	if exit != 0 || len(results) != 1 || results[0].Status != "passed" {
		t.Fatalf("exit = %d, results = %+v; want the command's own pass", exit, results)
	}
	if want := "cfo verify: cmd /d /c echo changed> moved.txt tested no one source: verify: the source differed between the reads as the run started and once it ended"; !strings.HasPrefix(stderr.String(), want) {
		t.Errorf("stderr = %q; want it to start %q", stderr.String(), want)
	}
	if _, err := results[0].Source.Tested(); err == nil {
		t.Errorf("the result's reads %+v name a tested source; want none", results[0].Source)
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
	runtime, taskTmp := measuredTask(t, `"verification": {"fast": [["git", "version"]]}`)
	saved := filepath.Join(taskTmp, "verification-fast.json")
	if err := os.WriteFile(saved, []byte(`[{"command": ["git", "version"], "exit_code": 0, "status": "passed"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
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
	whileWaiting := savedResults(t, saved)
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
	if len(whileWaiting) != 1 || whileWaiting[0].Status != "running" {
		t.Fatalf("saved while waiting = %+v, want the tier running in place of the earlier pass", whileWaiting)
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

// A security mode that asks for no scan of a change runs nothing and
// passes, with an empty record.
func TestSecurityRunsNothingWhenItsModeAsksForNoScan(t *testing.T) {
	// Arrange
	runtime, taskTmp := measuredTask(t, `"security": {"mode": "changed", "deep": [["git", "version"]]}`)

	// Act
	var stdout, stderr bytes.Buffer
	exit := runWithRuntime([]string{"security", "t1"}, &stdout, &stderr, runtime)

	// Assert
	if exit != 0 || stderr.Len() != 0 {
		t.Fatalf("exit = %d, stderr = %q", exit, stderr.String())
	}
	if results := savedResults(t, filepath.Join(taskTmp, "security.json")); len(results) != 0 {
		t.Fatalf("results = %+v, want none", results)
	}
}

// A tier that takes no turn overwrites what an earlier run saved, so an old
// pass never stands for it.
func TestVerifyClearsAnEarlierResultWhenItTakesNoTurn(t *testing.T) {
	// Arrange
	runtime, taskTmp := measuredTask(t, `"verification": {"fast": [["git", "version"]]}`)
	runtime.availableMemory = nil
	saved := filepath.Join(taskTmp, "verification-fast.json")
	if err := os.WriteFile(saved, []byte(`[{"command": ["git", "version"], "exit_code": 0}]`), 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	var stdout, stderr bytes.Buffer
	exit := runWithRuntime([]string{"verify", "t1"}, &stdout, &stderr, runtime)

	// Assert
	if exit != 1 || !strings.Contains(stderr.String(), "takes no turn") {
		t.Fatalf("exit = %d, stderr = %q", exit, stderr.String())
	}
	if results := savedResults(t, saved); len(results) != 1 || results[0].Status != "not_run" || !strings.Contains(results[0].Error, "takes no turn") {
		t.Fatalf("saved = %+v, want only that the tier ran nothing and why", results)
	}
}

// A tier that names no command saves that it ran nothing, so its file never
// reads as a pass or as nothing at all.
func TestVerifySavesThatATierWithNoCommandRanNothing(t *testing.T) {
	// Arrange
	runtime, taskTmp := measuredTask(t, `"verification": {"fast": []}`)

	// Act
	var stdout, stderr bytes.Buffer
	exit := runWithRuntime([]string{"verify", "t1"}, &stdout, &stderr, runtime)

	// Assert
	results := savedResults(t, filepath.Join(taskTmp, "verification-fast.json"))
	if exit != 1 || len(results) != 1 || results[0].Status != "not_run" {
		t.Fatalf("exit = %d, saved = %+v, want one not_run entry", exit, results)
	}
}

// Each command's saved result says how it ended.
func TestVerifySavesEachCommandsStatus(t *testing.T) {
	// Arrange
	runtime, taskTmp := measuredTask(t, `"verification": {"fast": [["git", "version"], ["git", "-c", "alias.fail=!exit 3", "fail"]]}`)

	// Act
	var stdout, stderr bytes.Buffer
	runWithRuntime([]string{"verify", "t1"}, &stdout, &stderr, runtime)

	// Assert
	results := savedResults(t, filepath.Join(taskTmp, "verification-fast.json"))
	if len(results) != 2 || results[0].Status != "passed" || results[1].Status != "failed" || results[1].ExitCode != 3 {
		t.Fatalf("saved = %+v, want passed then failed", results)
	}
}

// What a command leaves running past its linger is ended and named on
// standard error, and the tier keeps the command's own result.
func TestVerifyNamesWhatACommandLeftRunning(t *testing.T) {
	// Arrange
	runtime, taskTmp := measuredTask(t, `"verification": {"fast": [["cmd", "/d", "/c", "start", "/b", "ping", "-n", "60", "127.0.0.1"]]}`)

	// Act
	var stdout, stderr bytes.Buffer
	exit := runWithRuntime([]string{"verify", "t1"}, &stdout, &stderr, runtime)

	// Assert
	if exit != 0 || !strings.Contains(strings.ToLower(stderr.String()), "ping.exe pid") {
		t.Fatalf("exit = %d, stderr = %q, want a pass naming ping as left running", exit, stderr.String())
	}
	results := savedResults(t, filepath.Join(taskTmp, "verification-fast.json"))
	if len(results) != 1 || results[0].Status != "passed" || len(results[0].Leftovers) != 1 || !strings.EqualFold(results[0].Leftovers[0].Name, "ping.exe") {
		t.Fatalf("saved = %+v, want a pass that names ping as left running", results)
	}
}

// A tier with an empty command after a good one runs none of them, so its
// file never shows only a pass.
func TestVerifyRunsNothingOfATierWithAnEmptyCommand(t *testing.T) {
	// Arrange
	runtime, taskTmp := measuredTask(t, `"verification": {"fast": [["git", "version"], []]}`)

	// Act
	var stdout, stderr bytes.Buffer
	exit := runWithRuntime([]string{"verify", "t1"}, &stdout, &stderr, runtime)

	// Assert
	results := savedResults(t, filepath.Join(taskTmp, "verification-fast.json"))
	if exit != 1 || len(results) != 1 || results[0].Status != "not_run" || !strings.Contains(results[0].Error, "empty command") {
		t.Fatalf("exit = %d, saved = %+v, want the tier refused before anything ran", exit, results)
	}
}

// Each tier prunes the executions store, so its records and logs never grow
// past what it keeps.
func TestVerifyPrunesTheExecutionsStore(t *testing.T) {
	// Arrange
	runtime, taskTmp := measuredTask(t, `"verification": {"fast": [["git", "version"]]}`)
	executions := filepath.Join(os.Getenv("CFO_VERIFY_DIR"), "executions")
	if err := os.MkdirAll(executions, 0o700); err != nil {
		t.Fatal(err)
	}
	abandoned := filepath.Join(executions, "AAAAAAAAAAAAAAAAAAAAAAAAAA.json.stdout.log")
	if err := os.WriteFile(abandoned, []byte("log"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-30 * 24 * time.Hour)
	if err := os.Chtimes(abandoned, old, old); err != nil {
		t.Fatal(err)
	}

	// Act
	var stdout, stderr bytes.Buffer
	exit := runWithRuntime([]string{"verify", "t1"}, &stdout, &stderr, runtime)

	// Assert
	if exit != 0 || stderr.Len() != 0 {
		t.Fatalf("exit = %d, stderr = %q", exit, stderr.String())
	}
	if _, err := os.Stat(abandoned); !os.IsNotExist(err) {
		t.Fatalf("an abandoned log outlived the tier's pruning: %v", err)
	}
	results := savedResults(t, filepath.Join(taskTmp, "verification-fast.json"))
	if len(results) != 1 {
		t.Fatalf("saved = %+v", results)
	}
	for _, path := range []string{results[0].Execution, results[0].Execution + ".stdout.log", results[0].Execution + ".stderr.log"} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("the tier's own evidence %s was pruned: %v", path, err)
		}
	}
}

// A store the tier cannot prune is named on standard error, and the tier
// keeps its own result.
func TestVerifyKeepsItsResultWhenItCannotPruneTheStore(t *testing.T) {
	// Arrange
	runtime, taskTmp := measuredTask(t, `"verification": {"fast": [["git", "version"]]}`)
	executions := filepath.Join(os.Getenv("CFO_VERIFY_DIR"), "executions")
	if output, err := exec.Command("cmd", "/d", "/c", "mklink", "/J", executions, t.TempDir()).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J: %v %s", err, output)
	}

	// Act
	var stdout, stderr bytes.Buffer
	exit := runWithRuntime([]string{"verify", "t1"}, &stdout, &stderr, runtime)

	// Assert
	if exit != 0 || !strings.Contains(stderr.String(), "could not all be removed") || !strings.Contains(stderr.String(), "not a plain folder") {
		t.Fatalf("exit = %d, stderr = %q, want a pass that names the store it could not prune", exit, stderr.String())
	}
	if results := savedResults(t, filepath.Join(taskTmp, "verification-fast.json")); len(results) != 1 || results[0].Status != "passed" {
		t.Fatalf("saved = %+v, want the tier's own pass", results)
	}
}

// A machine service a tier's command started, as Docker Desktop serves the
// whole machine, is left running and named on standard error and in the
// command's entry, and the tier keeps its pass.
func TestVerifyLeavesAMachineServiceItsCommandStartedRunning(t *testing.T) {
	// Arrange
	programs := t.TempDir()
	standin.RemoveAtCleanup(t, programs)
	// A name of its own, so the test finds its stand-in among every process.
	image := "com.docker.standin-" + strings.ToLower(rand.Text()) + ".exe"
	service := filepath.Join(programs, image)
	standin.Put(t, service)
	for _, variable := range standin.Env("", standin.Rule{Any: true, Hold: true}) {
		name, value, _ := strings.Cut(variable, "=")
		t.Setenv(name, value)
	}
	command, err := json.Marshal([]string{"cmd", "/d", "/c", "start", "", "/b", service})
	if err != nil {
		t.Fatal(err)
	}
	runtime, taskTmp := measuredTask(t, `"verification": {"fast": [`+string(command)+`]}`)

	// Act
	var stdout, stderr bytes.Buffer
	exit := runWithRuntime([]string{"verify", "t1"}, &stdout, &stderr, runtime)

	// Assert
	processes, err := proc.Processes()
	if err != nil {
		t.Fatal(err)
	}
	// Whatever the run did, the stand-in it started ends with the test.
	for _, process := range processes {
		if strings.EqualFold(process.ExeBase, image) {
			standin.Hold(t, process.PID)
		}
	}
	results := savedResults(t, filepath.Join(taskTmp, "verification-fast.json"))
	if exit != 0 || !strings.Contains(strings.ToLower(stderr.String()), image+" pid") || !strings.Contains(stderr.String(), "which serves the whole machine, so the run left it running") {
		t.Fatalf("exit = %d, stderr = %q, want a pass naming the service left running", exit, stderr.String())
	}
	if len(results) != 1 || results[0].Status != "passed" || len(results[0].Spared) != 1 || len(results[0].Leftovers) != 0 || !strings.EqualFold(results[0].Spared[0].Name, image) {
		t.Fatalf("saved = %+v, want a pass that names the service as spared and nothing as ended", results)
	}
}

// A command that never started read no source, so the tier says nothing of
// its source, only that it took no turn.
func TestVerifySaysNothingOfTheSourceOfACommandThatNeverStarted(t *testing.T) {
	// Arrange
	runtime, _ := measuredTask(t, `"verification": {"fast": [["git", "version"]]}`)
	runtime.availableMemory = func() (supervisor.Memory, error) {
		return supervisor.Memory{}, errors.New("memory cannot be read")
	}

	// Act
	var stdout, stderr bytes.Buffer
	exit := runWithRuntime([]string{"verify", "t1"}, &stdout, &stderr, runtime)

	// Assert
	if exit != 1 || strings.Contains(stderr.String(), "tested no one source") {
		t.Errorf("exit = %d, stderr = %q; want a failure that says nothing of a source", exit, stderr.String())
	}
}
