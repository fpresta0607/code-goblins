package proc_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/proc"
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
	windowOwners, err := proc.WindowOwners()
	if err != nil {
		t.Fatal(err)
	}

	// Act
	isHiddenShown, hiddenErr := proc.ConsoleShown(hidden, windowOwners)
	// The hidden program started its console host, so where it showed a
	// window of its own its host's parent would.
	isInTerminal, terminalErr := proc.ConsoleShown(hidden, map[int]bool{hidden: true})
	isDetachedShown, detachedErr := proc.ConsoleShown(detached, map[int]bool{detached: true})

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

// shownWindows lists the visible top-level windows of the desktop, each
// with whether it is minimized.
func shownWindows(t *testing.T) map[windows.HWND]bool {
	t.Helper()
	isIconic := windows.NewLazySystemDLL("user32.dll").NewProc("IsIconic")
	found := map[windows.HWND]bool{}
	callback := syscall.NewCallback(func(window windows.HWND, _ uintptr) uintptr {
		if windows.IsWindowVisible(window) {
			minimized, _, _ := isIconic.Call(uintptr(window))
			found[window] = minimized != 0
		}
		return 1
	})
	if err := windows.EnumWindows(callback, nil); err != nil {
		t.Fatal(err)
	}
	return found
}

// A console a careless start opened shows on the desktop until HideConsole
// hides it: the console host's window goes, and Windows Terminal, shown a
// console handed to it, minimizes its window, so nothing of it is left in
// front of the Overlord. The program runs on. It shows a window, so it runs
// only on the hosted runner of .github/workflows/windows.yml, which names
// its default terminal in CFO_WINDOW_PROOF.
func TestHideConsoleTakesACarelessConsoleOffTheDesktop(t *testing.T) {
	terminal := os.Getenv("CFO_WINDOW_PROOF")
	if terminal == "" {
		t.Skip("shows a window on the desktop, so it runs only on the hosted runner of .github/workflows/windows.yml")
	}
	// Arrange
	before := shownWindows(t)
	command := exec.Command(filepath.Join(os.Getenv("SystemRoot"), "System32", "PING.EXE"), "-n", "60", "127.0.0.1")
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Process.Kill(); _ = command.Wait() })
	inFront := func() int {
		count := 0
		for window, isMinimized := range shownWindows(t) {
			if _, isOld := before[window]; !isOld && !isMinimized {
				count++
			}
		}
		return count
	}
	for deadline := time.Now().Add(30 * time.Second); inFront() == 0; time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("with %s as the default terminal, a new console never showed a window: the premise of this test does not hold", terminal)
		}
	}
	// The supervisor asks after a process's console from its second second
	// on, by when Windows Terminal has told the console that it shows it.
	time.Sleep(time.Second)

	// Act
	isHidden, err := proc.HideConsole(command.Process.Pid)

	// Assert
	if err != nil || !isHidden {
		host, hostErr := proc.ConsoleHost(command.Process.Pid)
		var shown []string
		for window := range shownWindows(t) {
			if _, isOld := before[window]; isOld {
				continue
			}
			var owner uint32
			_, _ = windows.GetWindowThreadProcessId(window, &owner)
			class := make([]uint16, 256)
			copied, _ := windows.GetClassName(window, &class[0], int32(len(class)))
			shown = append(shown, fmt.Sprintf("%s of pid %d", windows.UTF16ToString(class[:copied]), owner))
		}
		t.Fatalf("with %s as the default terminal, HideConsole(%d) = %t, %v; want a console window hidden. Its console host is pid %d (%v), and the new windows are %q", terminal, command.Process.Pid, isHidden, err, host, hostErr, shown)
	}
	time.Sleep(time.Second)
	if count := inFront(); count != 0 {
		t.Fatalf("with %s as the default terminal, %d new windows still show in front a second after HideConsole", terminal, count)
	}
	if _, isRunning := proc.StartTime(command.Process.Pid); !isRunning {
		t.Fatal("the program ended; want it running on")
	}
}

// A process with no console, or one whose console shows no window, has no
// console window to hide, and HideConsole says so without hiding anything.
func TestHideConsoleHidesNothingOfAConsoleWithNoWindow(t *testing.T) {
	// Arrange
	program := standin.Program(t)
	record := filepath.Join(t.TempDir(), "started")
	for _, flags := range []uint32{windows.CREATE_NO_WINDOW, windows.DETACHED_PROCESS} {
		command := exec.Command(program, "quiet")
		command.Env = append(os.Environ(), standin.Env(record, standin.Rule{Any: true, Hold: true})...)
		command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags}
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = command.Process.Kill(); _ = command.Wait() })

		// Act
		isHidden, err := proc.HideConsole(command.Process.Pid)

		// Assert
		if err != nil || isHidden {
			t.Errorf("flags %#x: HideConsole = %t, %v; want nothing hidden and no error", flags, isHidden, err)
		}
	}
}
