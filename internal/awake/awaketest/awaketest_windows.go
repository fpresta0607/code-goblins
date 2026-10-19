// Package awaketest pauses a test's process as a machine that pauses stops
// every process: a laptop's sleep, or the host of a hosted runner pausing its
// virtual machine (#1048). A test runs itself again in another process with
// Across, and that process, where IsPausedProcess is true, holds what it
// waits on and waits, saying Ready once the pause may fall. Across suspends
// every thread of it for the pause's length, so what it waits on stops with
// the wait, as on a paused machine, and the wall clock runs on.
package awaketest

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// variable makes a test binary the process a test pauses.
const variable = "CFO_AWAKE_TEST_PAUSED"

// readyLine is the line the paused process writes once the pause may fall.
const readyLine = "awaketest: ready"

// lifetime bounds how long the paused process may take to say it is ready,
// and to end after the pause.
const lifetime = time.Minute

// Length is how long Across pauses the process: longer than the second a
// step of a timeout must end late by to be a pause, and than the waits the
// tests that pause shorten theirs to, so a wait the pause spent runs out in
// it.
const Length = 2500 * time.Millisecond

var (
	ntdll            = windows.NewLazySystemDLL("ntdll.dll")
	ntSuspendProcess = ntdll.NewProc("NtSuspendProcess")
	ntResumeProcess  = ntdll.NewProc("NtResumeProcess")
)

// IsPausedProcess reports whether this process is the one Across pauses.
func IsPausedProcess() bool {
	return os.Getenv(variable) != ""
}

// Ready tells Across that the pause may fall now.
func Ready() {
	fmt.Println(readyLine)
}

// Across runs t's test again in another process, pauses that process for
// Length once it is Ready, and fails t unless the test passes there.
func Across(t *testing.T) {
	t.Helper()
	paused := execx.Command(os.Args[0], "-test.run=^"+regexp.QuoteMeta(t.Name())+"$", "-test.count=1", "-test.v")
	paused.Env = append(os.Environ(), variable+"=1")
	output, err := paused.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	paused.Stderr = paused.Stdout
	if err := paused.Start(); err != nil {
		t.Fatal(err)
	}
	// said and waited are written before ended closes.
	var said strings.Builder
	var waited error
	ready, ended := make(chan struct{}), make(chan struct{})
	go func() {
		lines := bufio.NewScanner(output)
		for lines.Scan() {
			said.WriteString(lines.Text() + "\n")
			if lines.Text() == readyLine {
				close(ready)
			}
		}
		waited = paused.Wait()
		close(ended)
	}()
	t.Cleanup(func() {
		_ = paused.Process.Kill()
		<-ended
	})

	select {
	case <-ready:
	case <-ended:
		t.Fatalf("the paused process ended before it was ready: %v\n%s", waited, said.String())
	case <-time.After(lifetime):
		t.Fatalf("the paused process was not ready within %s", lifetime)
	}
	pause(t, paused.Process.Pid)
	select {
	case <-ended:
		if waited != nil {
			t.Fatalf("the test failed across a pause of %s: %v\n%s", Length, waited, said.String())
		}
	case <-time.After(lifetime):
		t.Fatalf("the paused process did not end within %s of its pause", lifetime)
	}
}

// pause suspends every thread of process pid for Length and resumes them.
func pause(t *testing.T, pid int) {
	t.Helper()
	process, err := windows.OpenProcess(windows.PROCESS_SUSPEND_RESUME, false, uint32(pid))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(process)
	if status, _, _ := ntSuspendProcess.Call(uintptr(process)); status != 0 {
		t.Fatalf("NtSuspendProcess = %#x", status)
	}
	time.Sleep(Length)
	if status, _, _ := ntResumeProcess.Call(uintptr(process)); status != 0 {
		t.Fatalf("NtResumeProcess = %#x", status)
	}
}
