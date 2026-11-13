package main

import (
	"bytes"
	"errors"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// crashStateVariable names the state folder to the test binary started again
// as a window that crashes, and crashHasNoStderrVariable says it was started
// as Explorer starts the window, with no standard error of its own.
const (
	crashStateVariable       = "CODE_GOBLINS_WINDOW_CRASH_STATE"
	crashHasNoStderrVariable = "CODE_GOBLINS_WINDOW_CRASH_NO_STDERR"
)

// On 2026-10-13 at 01:41Z the Overlord's board window ended during a memory
// emergency and left nothing behind: started from Explorer it had no standard
// error, so its own last words went nowhere and window.log was days old. A
// window started with no standard error writes what it says, and the words of
// a crash, to window.log in the fleet's state folder, beside the supervisor's
// own serve.log; one goblins started already writes there and is left alone.
func TestAWindowThatEndsLeavesItsReasonInWindowLog(t *testing.T) {
	for _, testCase := range []struct {
		name                     string
		hasNoStderr, shouldStore bool
	}{
		{name: "started from Explorer", hasNoStderr: true, shouldStore: true},
		{name: "started by goblins", hasNoStderr: false, shouldStore: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			stateDir := t.TempDir()
			command := exec.Command(os.Args[0], "-test.run=^TestCrashingWindow$")
			command.Env = append(os.Environ(), crashStateVariable+"="+stateDir)
			if testCase.hasNoStderr {
				command.Env = append(command.Env, crashHasNoStderrVariable+"=1")
			}
			var stderr bytes.Buffer
			command.Stderr = &stderr

			// Act
			err := command.Run()

			// Assert
			var exit *exec.ExitError
			if !errors.As(err, &exit) {
				t.Fatalf("the crashing window ended with %v, want a crash", err)
			}
			kept, readErr := os.ReadFile(filepath.Join(stateDir, "window.log"))
			if !testCase.shouldStore {
				if !errors.Is(readErr, os.ErrNotExist) || !strings.Contains(stderr.String(), "panic: the window's last words") {
					t.Fatalf("a window goblins started wrote window.log itself (%v) or lost its standard error: %q", readErr, stderr.String())
				}
				return
			}
			if readErr != nil {
				t.Fatalf("the crash left no window.log: %v", readErr)
			}
			for _, want := range []string{"window started, pid", "about to crash", "panic: the window's last words", "goroutine "} {
				if !strings.Contains(string(kept), want) {
					t.Errorf("window.log lacks %q:\n%s", want, kept)
				}
			}
		})
	}
}

// TestCrashingWindow is the window the test above starts: it keeps its output
// as the window does once it knows the state folder, then crashes.
func TestCrashingWindow(t *testing.T) {
	stateDir := os.Getenv(crashStateVariable)
	if stateDir == "" {
		t.Skip("started only by TestAWindowThatEndsLeavesItsReasonInWindowLog")
	}
	if os.Getenv(crashHasNoStderrVariable) != "" {
		if err := windows.SetStdHandle(windows.STD_ERROR_HANDLE, 0); err != nil {
			t.Fatal(err)
		}
		if err := windows.SetStdHandle(windows.STD_OUTPUT_HANDLE, 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := keepOutput(stateDir); err != nil {
		t.Fatal(err)
	}
	log.Printf("about to crash")
	panic("the window's last words")
}
