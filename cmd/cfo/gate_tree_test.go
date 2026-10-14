package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx/treetest"
)

// gateTreeTestRunner is this binary as the runner of cfo gate test's checks
// or of cfo gate prepush's, by its second argument, running a tree's run in
// the folder its third names, as a process of its own that its caller can
// end.
const gateTreeTestRunner = "gate-tree-test-runner"

// runGateTreeTestProgram runs this binary as gateTreeTestRunner, or says
// that its arguments name no such program.
func runGateTreeTestProgram() (int, bool) {
	if len(os.Args) != 4 || os.Args[1] != gateTreeTestRunner {
		return 0, false
	}
	command := treeCommand(os.Args[3])
	switch os.Args[2] {
	case "test":
		exit, _ := runGateCommand(command, os.Args[3], os.Environ(), os.Stdout, os.Stderr)
		return exit, true
	case "prepush":
		exit, _ := runPushCommand(context.Background(), command, os.Args[3], os.Environ(), os.Stdout, os.Stderr)
		return exit, true
	}
	return 2, true
}

// treeCommand is the command that runs a tree's run in folder.
func treeCommand(folder string) []string {
	return append([]string{os.Args[0]}, treetest.Args(folder, false)...)
}

// foundTree is what findTree found of a tree, with why it found no more.
type foundTree struct {
	treetest.Found
	err error
}

// findTree finds the tree in folder in the background.
func findTree(folder string) <-chan foundTree {
	found := make(chan foundTree, 1)
	go func() {
		tree, err := treetest.Find(folder)
		found <- foundTree{tree, err}
	}()
	return found
}

// foundIn waits for the tree findTree finds, ends what it found when the
// test ends, and fails the test when it found no helper.
func foundIn(t *testing.T, found <-chan foundTree) treetest.Found {
	t.Helper()
	tree := <-found
	t.Cleanup(tree.End)
	if tree.err != nil {
		t.Fatalf("the tree was not found: %v", tree.err)
	}
	return tree.Found
}

// running lists, by pid, the processes of tree that still run a moment on.
func running(tree treetest.Found) []int {
	var pids []int
	for _, member := range tree.Members() {
		if !member.Ends() {
			pids = append(pids, member.PID)
		}
	}
	return pids
}

// named reports whether said names each of members by name and pid, as a
// run names what it ended.
func named(said string, members ...treetest.Member) bool {
	for _, member := range members {
		if !strings.Contains(said, fmt.Sprintf("%s pid %d", filepath.Base(os.Args[0]), member.PID)) {
			return false
		}
	}
	return true
}

// A check still running at the time limit is ended there with everything
// it started, and the run names what it ended by name and pid: on
// 2026-10-12 a check cut at the limit of 15 minutes left a test's helper
// running, a supervisor.test.exe whose test process had already ended.
func TestGatePrepushEndsEverythingACheckStartedAtTheTimeLimit(t *testing.T) {
	// Arrange
	dir := testStepModule(t,
		map[string]string{"config/verify.json": strings.Replace(quiet, `"slow_packages": ["a"]`, `"slow_packages": []`, 1), "a/a_test.go": passing},
		map[string]string{"a/a.go": "package a\n\nfunc A() int { return 2 }\n"})
	t.Chdir(dir)
	folder := t.TempDir()
	runtime := defaultCommandRuntime()
	runtime.availableMemory = plenty
	runtime.gateDisk = roomy
	starts := 0
	runtime.pushRun = func(ctx context.Context, _ []string, dir string, env []string, stdout, stderr io.Writer) (int, error) {
		if starts++; starts == 1 {
			return 0, nil
		}
		return runPushCommand(ctx, treeCommand(folder), dir, env, stdout, stderr)
	}
	found := findTree(folder)

	// Act
	var stdout, stderr bytes.Buffer
	exit := gatePrepushWith(runtime, &stdout, &stderr, "--limit", "5s")

	// Assert
	tree := foundIn(t, found)
	if exit != 0 || starts != 2 {
		t.Fatalf("exit = %d after %d start(s), want 0 after 2, cut at the limit; stdout=%s stderr=%s", exit, starts, stdout.String(), stderr.String())
	}
	if left := running(tree); len(left) > 0 {
		t.Errorf("pids %v of the check's tree still run, want the check, its child and the helper whose parent exited all ended at the limit", left)
	}
	if !named(stdout.String(), tree.Members()...) {
		t.Errorf("stdout %q does not name each of pids %d, %d and %d among what the limit ended", stdout.String(), tree.Run.PID, tree.Child.PID, tree.Helper.PID)
	}
	if said := stdout.String() + stderr.String(); strings.ContainsAny(said, ";\u2014\u2013") {
		t.Errorf("the run's own lines hold a semicolon or a long dash: %q", said)
	}
}

// A check of cfo gate test or of cfo gate prepush whose run is ended, as a
// gate step's limit or a goblin's tool ends cfo, ends with everything it
// started: nothing a check ran outlives the run.
func TestAnEndedGateRunEndsEverythingItsCheckStarted(t *testing.T) {
	for _, runner := range []string{"test", "prepush"} {
		t.Run(runner, func(t *testing.T) {
			// Arrange
			folder := t.TempDir()
			process := exec.Command(os.Args[0], gateTreeTestRunner, runner, folder)
			if err := process.Start(); err != nil {
				t.Fatal(err)
			}
			waited := make(chan struct{})
			go func() { process.Wait(); close(waited) }()
			t.Cleanup(func() { process.Process.Kill(); <-waited })
			tree := foundIn(t, findTree(folder))

			// Act
			if err := process.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			<-waited

			// Assert
			if left := running(tree); len(left) > 0 {
				t.Errorf("pids %v of the check's tree still run, want the check, its child and the helper whose parent exited all ended with the run", left)
			}
		})
	}
}

// A gate turn that is ended, as its caller's limit ends it, ends its
// command with everything the command started.
func TestAnEndedGateTurnEndsEverythingItsCommandStarted(t *testing.T) {
	// Arrange
	turnLine(t)
	folder := t.TempDir()
	run := startGateTurn(t, folder, nil, append([]string{"--"}, treeCommand(folder)...)...)
	tree := foundIn(t, findTree(folder))

	// Act
	if err := run.process.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	run.exit(t, "the run, once ended,")

	// Assert
	if left := running(tree); len(left) > 0 {
		t.Errorf("pids %v of the command's tree still run, want the command, its child and the helper whose parent exited all ended with the run", left)
	}
}

// Once its command has ended, interrupted or not, a run lets what the
// command left running go on for the linger, then ends it and names it by
// name and pid: what a command leaves is the run's, so the run's turn is
// never given back with it still running. Interrupted, the run lets the
// command answer for itself first.
func TestAGateTurnEndsWhatItsCommandLeftRunning(t *testing.T) {
	for _, test := range []struct {
		name          string
		isInterrupted bool
	}{
		{name: "interrupted", isInterrupted: true},
		{name: "exited by itself"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			turnLine(t)
			folder := t.TempDir()
			signals := make(chan os.Signal, 1)
			runtime := turnRuntime()
			runtime.interrupts = func() (<-chan os.Signal, func()) { return signals, func() {} }
			var stdout, stderr lockedBuffer
			exited := make(chan int, 1)
			go func() {
				exited <- gateTurn(runtime, &stdout, &stderr, append([]string{"--"}, treeCommand(folder)...)...)
			}()
			tree := foundIn(t, findTree(folder))

			// Act
			if test.isInterrupted {
				signals <- os.Interrupt
			}
			if err := treetest.Release(folder); err != nil {
				t.Fatal(err)
			}

			// Assert
			select {
			case exit := <-exited:
				if exit != 0 {
					t.Errorf("exit = %d, want the command's 0; stderr=%q", exit, stderr.String())
				}
			case <-time.After(time.Minute):
				t.Fatalf("the run had not ended a minute after its command was let go; stderr=%q", stderr.String())
			}
			if !tree.Helper.Ends() {
				t.Error("the helper the command left running still runs, want it ended once the command had ended")
			}
			if !named(stderr.String(), tree.Helper) {
				t.Errorf("stderr %q does not name the helper, pid %d, among what the run ended", stderr.String(), tree.Helper.PID)
			}
		})
	}
}
