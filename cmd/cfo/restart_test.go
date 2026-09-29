package main

import (
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

func TestRestartProcessHelper(t *testing.T) {
	if os.Getenv("CFO_TEST_RESTART_PROCESS") != "yes" {
		return
	}
	time.Sleep(time.Minute)
}

func TestRestartRefusesReusedPIDAndStopsOnlyTheRecordedProcess(t *testing.T) {
	start := func() *exec.Cmd {
		command := exec.Command(os.Args[0], "-test.run=^TestRestartProcessHelper$")
		command.Env = append(os.Environ(), "CFO_TEST_RESTART_PROCESS=yes")
		command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = command.Process.Kill(); _ = command.Wait() })
		return command
	}
	target, sibling := start(), start()
	created, ok := proc.StartTime(target.Process.Pid)
	if !ok {
		t.Fatal("cannot read fixture process identity")
	}
	hostname, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	identity := lock.Info{PID: target.Process.Pid, Start: created.Add(-time.Minute), Hostname: hostname}
	if err := stopCFOProcess(identity); err == nil || !strings.Contains(err.Error(), "reused") {
		t.Fatalf("reused identity=%v", err)
	}
	if _, ok := proc.StartTime(target.Process.Pid); !ok {
		t.Fatal("stopped mismatched process")
	}
	identity.Start = created
	if err := stopCFOProcess(identity); err != nil {
		t.Fatal(err)
	}
	if _, ok := proc.StartTime(sibling.Process.Pid); !ok {
		t.Fatal("stopped another process")
	}
}
