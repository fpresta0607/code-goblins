package pty

import (
	"errors"
	"io/fs"
	"math/bits"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// lowestThread is the mask of the lowest of the processor threads this test
// may run on, of more than one.
func lowestThread(t *testing.T) uintptr {
	t.Helper()
	var set unix.CPUSet
	if err := unix.SchedGetaffinity(0, &set); err != nil {
		t.Fatal(err)
	}
	if set.Count() < 2 {
		if os.Getenv("CI") == "true" {
			t.Fatalf("CI runs this test on %d processor thread, so a process kept to fewer went untested", set.Count())
		}
		t.Skipf("this test runs on %d processor thread, so no process can be kept to fewer", set.Count())
	}
	for thread := range bits.UintSize {
		if set.IsSet(thread) {
			return 1 << thread
		}
	}
	t.Skip("this test runs on no processor thread a mask names")
	return 0
}

// allowedList is the list of processor threads a /proc status names, or ""
// for a thread that has ended.
func allowedList(t *testing.T, status string) string {
	t.Helper()
	text, err := os.ReadFile(status)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, unix.ESRCH) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`(?m)^Cpus_allowed_list:\s*(\S+)$`).FindSubmatch(text)
	if match == nil {
		t.Fatalf("%s names no Cpus_allowed_list: %q", status, text)
	}
	return string(match[1])
}

// A terminal kept to processor threads runs its program and everything the
// program starts on those alone, and leaves every thread of the process that
// started it where it was.
func TestAProgramKeptToProcessorThreadsAndWhatItStartsRunOnThemAlone(t *testing.T) {
	// Arrange
	processors := lowestThread(t)
	want := strconv.Itoa(bits.TrailingZeros64(uint64(processors)))
	before := allowedList(t, "/proc/self/status")

	// Act
	terminal, written := startSpec(t, Spec{Args: []string{"sh", "-c", `sh -c 'grep Cpus_allowed_list /proc/self/status'; sleep 600`}, Cols: 80, Rows: 24, Processors: processors})

	// Assert
	if got := allowedList(t, "/proc/"+strconv.Itoa(terminal.PID())+"/status"); got != want {
		t.Fatalf("the program may run on the processor threads %s, want %s", got, want)
	}
	if got := written.await(t, `Cpus_allowed_list:\s*(\S+)`)[1]; got != want {
		t.Fatalf("what the program started may run on the processor threads %s, want %s", got, want)
	}
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		tasks, err := filepath.Glob("/proc/self/task/*/status")
		if err != nil {
			t.Fatal(err)
		}
		kept := ""
		for _, task := range tasks {
			if got := allowedList(t, task); got != "" && got != before {
				kept = task + " may run on " + got
			}
		}
		if kept == "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("a thread of this process stayed kept to the program's threads, %s, not %s", kept, before)
		}
	}
}
