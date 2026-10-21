package supervisor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

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

// hideConsolePIDVariable names the console program's pid to
// TestHideConsoleOnDesktopFixture.
const hideConsolePIDVariable = "CFO_TEST_HIDE_CONSOLE_PID"

// TestHideConsoleOnDesktopFixture is not a test: it is the supervisor
// TestHideConsoleOnDesktopLeavesAConsoleAboveItsReach starts, which prints
// what hideConsoleOnDesktop answered.
func TestHideConsoleOnDesktopFixture(t *testing.T) {
	pid, err := strconv.Atoi(os.Getenv(hideConsolePIDVariable))
	if err != nil {
		return
	}
	isHidden, err := hideConsoleOnDesktop(pid)
	fmt.Printf("hidden=%t err=%v\n", isHidden, err)
	os.Exit(0)
}

// Windows lets no process attach to the console of one that runs above its
// integrity level, as a program the Overlord let run as an administrator
// runs above the supervisor, so its console is out of the supervisor's
// reach, and the supervisor leaves it without failing (#1189). Here the
// supervisor runs at low integrity, below a console program at this test's
// own, so nothing asks for an administrator's rights, and the console shows
// no window.
func TestHideConsoleOnDesktopLeavesAConsoleAboveItsReach(t *testing.T) {
	// Arrange
	program := standin.Program(t)
	record := filepath.Join(t.TempDir(), "started")
	console := exec.Command(program, "quiet")
	console.Env = append(os.Environ(), standin.Env(record, standin.Rule{Any: true, Hold: true})...)
	console.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	if err := console.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = console.Process.Kill(); _ = console.Wait() })
	// A stand-in records itself once it runs, by when its console is its.
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if data, _ := os.ReadFile(record); len(data) != 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the stand-in never recorded that it runs")
		}
	}
	var own windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_DUPLICATE|windows.TOKEN_QUERY|windows.TOKEN_ADJUST_DEFAULT|windows.TOKEN_ASSIGN_PRIMARY, &own); err != nil {
		t.Fatal(err)
	}
	defer own.Close()
	var below windows.Token
	if err := windows.DuplicateTokenEx(own, 0, nil, windows.SecurityImpersonation, windows.TokenPrimary, &below); err != nil {
		t.Fatal(err)
	}
	defer below.Close()
	low, err := windows.CreateWellKnownSid(windows.WinLowLabelSid)
	if err != nil {
		t.Fatal(err)
	}
	label := windows.Tokenmandatorylabel{Label: windows.SIDAndAttributes{Sid: low, Attributes: windows.SE_GROUP_INTEGRITY}}
	if err := windows.SetTokenInformation(below, windows.TokenIntegrityLevel, (*byte)(unsafe.Pointer(&label)), label.Size()); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	pid := strconv.Itoa(console.Process.Pid)
	reader := exec.Command(self, pid)
	reader.Args[0] = proc.ConsoleWindowRole
	reader.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS, Token: syscall.Token(below)}
	if output, err := reader.CombinedOutput(); err == nil || !strings.Contains(string(output), windows.ERROR_ACCESS_DENIED.Error()) {
		t.Fatalf("a console reader below the console's program read %q, %v; want Windows to refuse it: the premise of this test does not hold", output, err)
	}
	supervisor := exec.Command(self, "-test.run=^TestHideConsoleOnDesktopFixture$")
	supervisor.Env = append(os.Environ(), hideConsolePIDVariable+"="+pid)
	supervisor.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS, Token: syscall.Token(below)}
	var stderr strings.Builder
	supervisor.Stderr = &stderr

	// Act
	output, err := supervisor.Output()

	// Assert
	if got := strings.TrimSpace(string(output)); err != nil || got != "hidden=false err=<nil>" {
		t.Fatalf("hideConsoleOnDesktop below the console's program answered %q (%v: %s); want nothing hidden and no error", got, err, stderr.String())
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
