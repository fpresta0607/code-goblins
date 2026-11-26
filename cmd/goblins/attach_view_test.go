//go:build windows || linux || darwin

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// The attach test runs this binary as the program in a native terminal, and
// as goblins attach in a console of its own; TestMain sends each to its part.
const (
	attachTestTerminal = "attach-test-terminal"
	attachTestViewer   = "attach-test-viewer"
)

// attachTestView is goblins attach for the state directory and arguments it is
// given, with the test's home in place of the fleet's.
func attachTestView(stateDir string, args []string) int {
	runtime := commandRuntime{
		resolveHome:        func() (home.Home, error) { return home.Home{Root: filepath.Dir(stateDir), State: stateDir}, nil },
		nativeCEO:          supervisor.NativeCEO,
		liveCEO:            supervisor.LiveCEO,
		nativeTerminalRuns: supervisor.NativeTerminalRuns,
		attachNative:       attachNative,
	}
	return runAttach(args, os.Stdout, os.Stderr, runtime)
}

// hostAttachTestTerminal hosts native terminal id in this test process,
// running attachTestProgram, and closes it when the test ends.
func hostAttachTestTerminal(t *testing.T, stateDir, id string) {
	t.Helper()
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ended := make(chan struct{})
	go func() {
		defer close(ended)
		_ = host.Run(stateDir, host.Spec{ID: id, Args: []string{program, attachTestTerminal}, Cols: 80, Rows: 24})
	}()
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(10 * time.Millisecond) {
		if _, err := host.ReadRecord(stateDir, id); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the host never recorded itself")
		}
	}
	t.Cleanup(func() {
		if record, err := host.ReadRecord(stateDir, id); err == nil {
			if client, err := host.Dial(record); err == nil {
				_ = client.CloseTerminal()
				_ = client.Close()
			}
		}
		select {
		case <-ended:
		case <-time.After(time.Minute):
			t.Error("the terminal's host did not end")
		}
	})
}
