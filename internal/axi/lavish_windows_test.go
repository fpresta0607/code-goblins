package axi

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
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

// #915, 2026-10-14: lavish-axi's page server, which the first lavish-axi call
// starts and which outlives it, ran in a goblin's worktree, where the goblin's
// notify had run that call, and held the folder as its working directory, so
// cfo cleanup could not remove it. Every call runs in the user's profile.
func TestLavishRunsInTheProfileNotTheCallersFolder(t *testing.T) {
	// Arrange
	bin := t.TempDir()
	ranIn := filepath.Join(bin, "ran-in.txt")
	script := "@echo off\r\ncd > \"" + ranIn + "\"\r\necho session:\r\necho   url: \"http://127.0.0.1:4387/session/c42fd9a0de5ec55d\"\r\n"
	if err := os.WriteFile(filepath.Join(bin, "lavish-axi.cmd"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+filepath.Join(os.Getenv("SystemRoot"), "System32"))
	profile := t.TempDir()
	t.Setenv("USERPROFILE", profile)
	t.Chdir(t.TempDir())

	// Act
	_, err := (Lavish{Commands: execx.OSRunner{}}).Open(context.Background(), filepath.Join(bin, "plan.html"))

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(ranIn)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(data)); !fsx.SamePath(got, profile) {
		t.Errorf("lavish-axi ran in %q; want the profile %q", got, profile)
	}
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
		_, err := (Lavish{Commands: execx.OSRunner{}}).Poll(ctx, "plan.html", "", time.Minute)
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
