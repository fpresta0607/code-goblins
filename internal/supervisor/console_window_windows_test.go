package supervisor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/standin"
)

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

// A console a careless start opened shows on the desktop until the
// supervisor hides it: the console host's window goes, and Windows
// Terminal, shown a console handed to it, minimizes its window, so nothing
// of it is left in front of the Overlord. The program runs on. Each run
// after the first finds Windows Terminal running, which shows the console
// through a pseudo console window that neither the program nor its console
// host owns, as a hosted runner showed in 3 of 5 runs. It shows a window,
// so it runs only on the hosted runner of .github/workflows/windows.yml,
// which names its default terminal in CFO_WINDOW_PROOF.
func TestHideConsoleOnDesktopTakesACarelessConsoleOffTheDesktop(t *testing.T) {
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
	isHidden, err := hideConsoleOnDesktop(command.Process.Pid)

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
		t.Fatalf("with %s as the default terminal, hideConsoleOnDesktop(%d) = %t, %v; want a console window hidden. Its console host is pid %d (%v), and the new windows are %q", terminal, command.Process.Pid, isHidden, err, host, hostErr, shown)
	}
	time.Sleep(time.Second)
	if count := inFront(); count != 0 {
		t.Fatalf("with %s as the default terminal, %d new windows still show in front a second after hideConsoleOnDesktop", terminal, count)
	}
	if _, isRunning := proc.StartTime(command.Process.Pid); !isRunning {
		t.Fatal("the program ended; want it running on")
	}
}

// A process with no console, or one whose console shows no window, has no
// console window to hide, and the supervisor says so without hiding anything.
func TestHideConsoleOnDesktopHidesNothingOfAConsoleWithNoWindow(t *testing.T) {
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
		isHidden, err := hideConsoleOnDesktop(command.Process.Pid)

		// Assert
		if err != nil || isHidden {
			t.Errorf("flags %#x: hideConsoleOnDesktop = %t, %v; want nothing hidden and no error", flags, isHidden, err)
		}
	}
}
