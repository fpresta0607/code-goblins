package execx_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/execx/treetest"
)

// This test binary is also each process of the trees its tests run.
func TestMain(m *testing.M) {
	treetest.Run()
	os.Exit(m.Run())
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
			run, helper := tree.tree.Run, tree.tree.Helper
			if !errors.Is(err, context.Canceled) {
				t.Errorf("error = %v, want the run's cancellation", err)
			}
			if !run.Ends() || !helper.Ends() {
				t.Errorf("the run's process is running = %t and its helper whose parent exited is running = %t, want both ended with the run", run.IsRunning(), helper.IsRunning())
			}
			name := filepath.Base(os.Args[0])
			for _, member := range []treetest.Member{run, helper} {
				if said := fmt.Sprintf("%s pid %d", name, member.PID); err == nil || !strings.Contains(err.Error(), said) {
					t.Errorf("error = %v, want it to name %q among what the run ended", err, said)
				}
			}
		})
	}
}
