package proc

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TerminateVerified ends a process only when the one handle it ends it by
// shows the start time and the command line it was asked about.
func TestTerminateVerifiedEndsOnlyTheProcessItWasAskedAbout(t *testing.T) {
	cmd := exec.Command("cmd", "/c", "ping -n 60 127.0.0.1 >NUL")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-exited
	})
	start, ok := StartTime(cmd.Process.Pid)
	if !ok {
		t.Fatal("read the child's start time")
	}
	running := func() bool {
		select {
		case <-exited:
			return false
		case <-time.After(200 * time.Millisecond):
			return true
		}
	}
	var seen []string
	pingRole := func(arguments []string) error {
		seen = arguments
		if len(arguments) > 0 && strings.EqualFold(arguments[0], "cmd") {
			return nil
		}
		return errors.New("not the process asked about")
	}

	if err := TerminateVerified(cmd.Process.Pid, start.Add(-time.Hour), pingRole); err == nil || !running() {
		t.Fatalf("a process whose start time differs was ended (err %v)", err)
	}
	if err := TerminateVerified(cmd.Process.Pid, start, func([]string) error { return errors.New("refused") }); err == nil || !running() {
		t.Fatalf("a process whose command line was refused was ended (err %v)", err)
	}
	if err := TerminateVerified(cmd.Process.Pid, start, pingRole); err != nil {
		t.Fatalf("TerminateVerified = %v, want the process ended", err)
	}
	if running() {
		t.Fatal("the process asked about still runs")
	}
	if len(seen) < 2 || !strings.EqualFold(seen[1], "/c") {
		t.Errorf("command line read = %q, want cmd /c ...", seen)
	}
}
