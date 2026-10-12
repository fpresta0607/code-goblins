package execx_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/execx/treetest"
	"github.com/fpresta0607/code-goblins/internal/standin"
)

// treeRunner is this test binary as a runner of a tree in the folder its
// second argument names: it runs the tree as a KillTree request and cuts it
// once the folder holds a file named cut, for a minute at most.
const treeRunner = "tree-runner"

// This test binary is also each process of the trees its tests run, and the
// runner of one.
func TestMain(m *testing.M) {
	treetest.Run()
	if len(os.Args) == 3 && os.Args[1] == treeRunner {
		os.Exit(runTree(os.Args[2]))
	}
	os.Exit(m.Run())
}

func runTree(folder string) int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		defer cancel()
		for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if _, err := os.Stat(filepath.Join(folder, "cut")); err == nil {
				return
			}
		}
	}()
	_, err := (execx.OSRunner{}).Stream(ctx, execx.Request{Name: os.Args[0], Args: treetest.Args(folder, false), KillTree: true}, io.Discard, io.Discard)
	fmt.Println(err)
	return 0
}

// assertEnded fails the test unless every process of tree ends, and unless
// err names each by name and pid among what the run ended.
func assertEnded(t *testing.T, tree treetest.Found, err error) {
	t.Helper()
	name := filepath.Base(os.Args[0])
	for _, member := range tree.Members() {
		if !member.Ends() {
			t.Errorf("%s pid %d still runs, want it ended with the run", name, member.PID)
		}
		if said := fmt.Sprintf("%s pid %d", name, member.PID); err == nil || !strings.Contains(err.Error(), said) {
			t.Errorf("error = %v, want it to name %q among what the run ended", err, said)
		}
	}
}

// A run its context ends is ended with everything it started that still
// runs, and the error says what that was, by name and pid. On 2026-10-12
// cfo gate prepush ended a check at its time limit and a test's helper ran
// on, since the test process that started it had already ended: ending a
// process with what its parents reach left the helper unreached. A helper
// that broke away from a terminal's job of its own, as what Git Bash starts
// does, is reached too.
func TestOSRunnerStreamEndsEverythingTheRunStartedWhenItsContextEnds(t *testing.T) {
	for _, c := range []struct {
		name        string
		isBreakaway bool
	}{
		{name: "a helper whose parent has exited"},
		{name: "a helper that broke away from a terminal's job", isBreakaway: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			folder := t.TempDir()
			request := execx.Request{Name: os.Args[0], Args: treetest.Args(folder, c.isBreakaway), KillTree: true}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			type result struct {
				tree treetest.Found
				err  error
			}
			found := make(chan result, 1)
			go func() {
				defer cancel()
				tree, err := treetest.Find(folder)
				found <- result{tree, err}
			}()

			// Act
			_, err := (execx.OSRunner{}).Stream(ctx, request, io.Discard, io.Discard)

			// Assert
			tree := <-found
			t.Cleanup(tree.tree.End)
			if tree.err != nil {
				t.Fatal(tree.err)
			}
			if !errors.Is(err, context.Canceled) {
				t.Errorf("error = %v, want the run's cancellation", err)
			}
			assertEnded(t, tree.tree, err)
		})
	}
}

// A run started under a machine service, as a gate agent's cfo gate prepush
// runs under the no-mistakes daemon, is ended whole all the same: the
// service above the run is no part of the run's tree, and once made every
// process of the tree read as the service's, which the end then spared.
func TestOSRunnerStreamEndsTheTreeOfARunStartedUnderAMachineService(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	standin.RemoveAtCleanup(t, dir)
	// A stand-in that reads as Docker Desktop by its name, held running.
	service := exec.Command(filepath.Join(dir, "com.docker.standin.exe"))
	standin.Put(t, service.Path)
	service.Env = append(os.Environ(), standin.Env("", standin.Rule{Any: true, Hold: true})...)
	if err := service.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Process.Kill(); _ = service.Wait() })
	parent, err := windows.OpenProcess(windows.PROCESS_CREATE_PROCESS|windows.PROCESS_DUP_HANDLE, false, uint32(service.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(parent)
	folder := t.TempDir()
	var said strings.Builder
	runner := exec.Command(os.Args[0], treeRunner, folder)
	runner.Stdout = &said
	runner.SysProcAttr = &syscall.SysProcAttr{ParentProcess: syscall.Handle(parent)}
	if err := runner.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runner.Process.Kill() })
	waited := make(chan error, 1)
	go func() { waited <- runner.Wait() }()
	tree, err := treetest.Find(folder)
	t.Cleanup(tree.End)
	if err != nil {
		t.Fatal(err)
	}

	// Act
	if err := os.WriteFile(filepath.Join(folder, "cut"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	// Assert
	select {
	case err := <-waited:
		if err != nil {
			t.Fatalf("the runner failed: %v; it said %q", err, said.String())
		}
	case <-time.After(time.Minute):
		t.Fatal("the runner had not ended a minute after its cut")
	}
	assertEnded(t, tree, errors.New(said.String()))
}
