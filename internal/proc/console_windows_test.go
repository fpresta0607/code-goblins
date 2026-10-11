package proc

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/standin"
)

// A console program the fleet starts hidden has a console host of its own
// that shows no window. Its console shows only where its host shows a
// window, as a classic console window's does, or where the program that
// started the host does, as Windows Terminal does for a console in one of
// its tabs. A process with no console shows none. Nothing here shows on the
// screen: the windows a console would show in are given, never made.
func TestConsoleShownFollowsTheConsoleHostToItsWindow(t *testing.T) {
	// Arrange
	program := standin.Program(t)
	record := filepath.Join(t.TempDir(), "started")
	start := func(name string, flags uint32) int {
		t.Helper()
		command := exec.Command(program, name)
		command.Env = append(os.Environ(), standin.Env(record, standin.Rule{Any: true, Hold: true})...)
		command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags}
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = command.Process.Kill(); _ = command.Wait() })
		return command.Process.Pid
	}
	hidden := start("hidden", windows.CREATE_NO_WINDOW)
	detached := start("detached", windows.DETACHED_PROCESS)
	// A stand-in records itself once it runs, by when its console is its.
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if data, _ := os.ReadFile(record); strings.Count(string(data), "\n") >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the stand-ins never recorded that they run")
		}
	}
	windowOwners, err := WindowOwners()
	if err != nil {
		t.Fatal(err)
	}

	// Act
	isHiddenShown, hiddenErr := ConsoleShown(hidden, windowOwners)
	// The hidden program started its console host, so where it showed a
	// window of its own its host's parent would.
	isInTerminal, terminalErr := ConsoleShown(hidden, map[int]bool{hidden: true})
	isDetachedShown, detachedErr := ConsoleShown(detached, map[int]bool{detached: true})

	// Assert
	if hiddenErr != nil || terminalErr != nil || detachedErr != nil {
		t.Fatalf("ConsoleShown failed: %v, %v, %v", hiddenErr, terminalErr, detachedErr)
	}
	if isHiddenShown {
		t.Error("a console started with no window reads as shown")
	}
	if !isInTerminal {
		t.Error("a console whose host's parent shows a window reads as hidden")
	}
	if isDetachedShown {
		t.Error("a process with no console reads as showing one")
	}
}
