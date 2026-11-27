package main

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/standin"
)

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

// A store the tier cannot prune is named on standard error, and the tier
// keeps its own result.
func TestVerifyKeepsItsResultWhenItCannotPruneTheStore(t *testing.T) {
	// Arrange
	runtime, taskTmp := measuredTask(t, `"verification": {"fast": [["git", "version"]]}`)
	executions := filepath.Join(os.Getenv("GOBLINS_VERIFY_DIR"), "executions")
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
