package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

// probeConsole writes how many processes share this process's console, none
// when it has no console, whether that console has a window at all, and
// whether the window shows.
func probeConsole(report string) int {
	processes := make([]uint32, 16)
	count, _, _ := getConsoleProcessList.Call(uintptr(unsafe.Pointer(&processes[0])), uintptr(len(processes)))
	window, _, _ := getConsoleWindow.Call()
	visible := false
	if window != 0 {
		shown, _, _ := isWindowVisible.Call(window)
		visible = shown != 0
	}
	if err := os.WriteFile(report, []byte(fmt.Sprintf("processes=%d window=%t visible=%t", count, window != 0, visible)), 0o600); err != nil {
		return 1
	}
	return 0
}

// consoleProbeVariable turns this test binary into a stand-in for goblins serve
// that reports the console it was given to the file the variable names.
const consoleProbeVariable = "GOBLINS_TEST_CONSOLE_PROBE"

// The supervisor goblins starts must have a console of its own with no
// window: with none, every console program it runs would open a window of its
// own, and with the terminal's, closing the terminal would end it. A console
// with a window, even a hidden one, is one Windows can hand to Windows
// Terminal as the default terminal, which shows it, and closing that window
// ends the supervisor; the window it hands over reads as hidden here.
func TestDetachedStartGivesAHiddenConsoleOfItsOwn(t *testing.T) {
	dir := t.TempDir()
	report := filepath.Join(dir, "console.txt")
	t.Setenv(consoleProbeVariable, report)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	command, err := startDetached(executable, dir, filepath.Join(dir, "probe.log"))
	if err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- command.Wait() }()
	select {
	case err := <-exited:
		if err != nil {
			t.Fatalf("the stand-in failed: %v", err)
		}
	case <-time.After(30 * time.Second):
		_ = command.Process.Kill()
		t.Fatalf("the stand-in, pid %d, did not exit", command.Process.Pid)
	}

	data, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "processes=1 window=false visible=false" {
		t.Fatalf("stand-in console: %s, want a console with no window that only it is attached to", got)
	}
}

// serveConsoleReleaseVariable turns this test binary, started as goblins
// serve, into a stand-in that writes its pid beside the file the variable
// names and keeps its console until that file appears.
const serveConsoleReleaseVariable = "GOBLINS_TEST_SERVE_CONSOLE_RELEASE"

// holdServeConsole is that stand-in's whole run, and its exit code.
func holdServeConsole(release string) int {
	if err := os.WriteFile(release+".pid", []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		return 1
	}
	for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if _, err := os.Stat(release); err == nil {
			return 0
		}
	}
	return 1
}

// The supervisor goblins starts from a native terminal, and the console host
// Windows gives its hidden console, carry none of that terminal's id and
// proof value: Windows makes the console host from the environment serve
// starts with, before serve forgets them, and a process carrying a
// terminal's proof reads as that terminal's own, so its teardown could take
// the supervisor's console host for its leftover (#1717).
func TestADetachedServeAndItsConsoleHostCarryNoTerminalProof(t *testing.T) {
	// Arrange
	h, release := serveFromANativeTerminal(t)

	// Act
	exited, err := startDetachedServe(h)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.WriteFile(release, nil, 0o600)
		select {
		case <-exited:
		case <-time.After(30 * time.Second):
			t.Error("the stand-in serve did not exit")
		}
	})

	// Assert
	assertServeCarriesNoTerminalProof(t, release)
}

// The supervisor the update restarts from a native terminal, created
// suspended so the watcher lock is handed to it before it runs, carries none
// of that terminal's id and proof value either, nor does the console host
// Windows makes for it before it is let run (#1717).
func TestAnUpdateRestartedServeAndItsConsoleHostCarryNoTerminalProof(t *testing.T) {
	// Arrange
	h, release := serveFromANativeTerminal(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { releaseWatchLock(h) })

	// Act
	started, err := startSupervisor(h, executable, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.WriteFile(release, nil, 0o600)
		if !awaitExit(started, 30*time.Second) {
			t.Errorf("the stand-in serve, pid %d, did not exit", started.pid)
		}
	})

	// Assert
	assertServeCarriesNoTerminalProof(t, release)
}

// serveFromANativeTerminal is a home in a temporary directory, with this
// process carrying a native terminal's id and proof value, and with serve
// started from this test binary being the stand-in holdServeConsole runs
// until the release file it returns appears.
func serveFromANativeTerminal(t *testing.T) (home.Home, string) {
	t.Helper()
	dir := t.TempDir()
	h := home.Home{Root: dir, State: filepath.Join(dir, "state")}
	if err := os.MkdirAll(h.State, 0o700); err != nil {
		t.Fatal(err)
	}
	release := filepath.Join(dir, "release")
	t.Setenv(serveConsoleReleaseVariable, release)
	t.Setenv(host.IDVariable, "ceo")
	t.Setenv(host.ProofVariable, "terminal-proof")
	return h, release
}

// assertServeCarriesNoTerminalProof finds the stand-in serve holding release
// and its console host, and fails unless both run with the environment serve
// was started with and neither carries a native terminal's id or proof value.
func assertServeCarriesNoTerminalProof(t *testing.T, release string) {
	t.Helper()
	serve := 0
	for deadline := time.Now().Add(30 * time.Second); serve == 0 && time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if data, err := os.ReadFile(release + ".pid"); err == nil {
			serve, _ = strconv.Atoi(string(data))
		}
	}
	if serve == 0 {
		t.Fatal("the stand-in serve never wrote its pid")
	}
	consoleHost := 0
	for deadline := time.Now().Add(30 * time.Second); consoleHost == 0 && time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		entries, err := proc.Processes()
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.ParentPID == serve && strings.EqualFold(entry.ExeBase, "conhost.exe") {
				consoleHost = entry.PID
			}
		}
	}
	if consoleHost == 0 {
		t.Fatalf("the stand-in serve, pid %d, has no console host", serve)
	}
	for name, pid := range map[string]int{"serve": serve, "its console host": consoleHost} {
		_, _, environment, err := proc.ParametersAndEnvironment(pid)
		if err != nil {
			t.Fatalf("read the environment of %s, pid %d: %v", name, pid, err)
		}
		if !slices.Contains(environment, serveConsoleReleaseVariable+"="+release) {
			t.Fatalf("the environment read for %s, pid %d, is not the one goblins started serve with:\n%s", name, pid, strings.Join(environment, "\n"))
		}
		for _, entry := range environment {
			if variable, _, _ := strings.Cut(entry, "="); strings.EqualFold(variable, host.IDVariable) || strings.EqualFold(variable, host.ProofVariable) {
				t.Errorf("%s, pid %d, carries %s", name, pid, entry)
			}
		}
	}
}

var (
	getConsoleProcessList = syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleProcessList")
	getConsoleWindow      = syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleWindow")
	isWindowVisible       = syscall.NewLazyDLL("user32.dll").NewProc("IsWindowVisible")
)
