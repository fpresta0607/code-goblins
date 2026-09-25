package axi

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// TestLavishPollChild is the process a stand-in lavish-axi poll starts under
// its .cmd shim, as npm's shim starts node: it records its pid and waits.
func TestLavishPollChild(t *testing.T) {
	pidFile := os.Getenv("LAVISH_POLL_CHILD_PID")
	if pidFile == "" {
		return
	}
	if os.WriteFile(pidFile+".tmp", []byte(strconv.Itoa(os.Getpid())), 0o600) != nil || os.Rename(pidFile+".tmp", pidFile) != nil {
		os.Exit(9)
	}
	time.Sleep(time.Minute)
	os.Exit(0)
}

// A cancelled poll ends lavish-axi's whole process tree, not only its .cmd
// shim, so no poll lives on to take the Overlord's feedback.
func TestACancelledLavishPollEndsItsWholeProcessTree(t *testing.T) {
	bin := t.TempDir()
	pidFile := filepath.Join(bin, "child.pid")
	script := "@echo off\r\nset LAVISH_POLL_CHILD_PID=" + pidFile + "\r\n\"" + os.Args[0] + "\" -test.run=TestLavishPollChild\r\n"
	if err := os.WriteFile(filepath.Join(bin, "lavish-axi.cmd"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+filepath.Join(os.Getenv("SystemRoot"), "System32"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	polled := make(chan error, 1)
	go func() {
		_, err := (Lavish{Commands: execx.OSRunner{}}).Poll(ctx, "plan.html", time.Minute)
		polled <- err
	}()
	pid := 0
	for deadline := time.Now().Add(10 * time.Second); pid == 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the stand-in poll never started its child")
		}
		data, _ := os.ReadFile(pidFile)
		pid, _ = strconv.Atoi(string(data))
	}
	child, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() {
		_, _ = child.Wait()
		close(exited)
	}()
	t.Cleanup(func() {
		_ = child.Kill()
		<-exited
	})

	cancel()
	<-polled

	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatalf("the poll's child %d outlived the cancelled poll", pid)
	}
}
