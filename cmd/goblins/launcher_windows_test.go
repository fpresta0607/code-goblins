package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
	"unsafe"
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

var (
	getConsoleProcessList = syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleProcessList")
	getConsoleWindow      = syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleWindow")
	isWindowVisible       = syscall.NewLazyDLL("user32.dll").NewProc("IsWindowVisible")
)
