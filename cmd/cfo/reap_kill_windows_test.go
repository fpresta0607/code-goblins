package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/standin"
)

// TestKillTreeFixture is a process tree for killTree's test, by the role
// CFO_KILL_TREE_ROLE names: the root starts a stand-in Docker Desktop and a
// child of its own, the stand-in starts a backend, and each reports what it
// started to the file CFO_KILL_TREE_PIDS names and waits.
func TestKillTreeFixture(t *testing.T) {
	pids := os.Getenv("CFO_KILL_TREE_PIDS")
	start := func(program, role string) int {
		child := exec.Command(program, "-test.run=^TestKillTreeFixture$")
		child.Env = append(os.Environ(), "CFO_KILL_TREE_ROLE="+role)
		child.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS}
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		return child.Process.Pid
	}
	report := func(lines ...string) {
		file, err := os.OpenFile(pids, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		if _, err := file.WriteString(strings.Join(lines, "\n") + "\n"); err != nil {
			t.Fatal(err)
		}
	}
	switch os.Getenv("CFO_KILL_TREE_ROLE") {
	case "root":
		report("service "+strconv.Itoa(start(os.Getenv("CFO_KILL_TREE_SERVICE"), "service")), "own "+strconv.Itoa(start(os.Args[0], "sleep")))
	case "service":
		report("backend " + strconv.Itoa(start(os.Getenv("CFO_KILL_TREE_BINARY"), "sleep")))
	case "sleep":
	default:
		return
	}
	time.Sleep(time.Minute)
}

// cfo reap --force and goblins stop --force end a process with everything
// under it, and a harness left behind may have started Docker Desktop for a
// test. Docker Desktop serves the whole machine, so it is left running with
// its backend while the rest of the tree ends. The Docker Desktop here is a
// stand-in, a copy of this test binary under its name; never the real one.
func TestKillTreeLeavesAMachineServiceUnderTheProcessRunning(t *testing.T) {
	// Arrange
	programs := t.TempDir()
	standin.RemoveAtCleanup(t, programs)
	binary, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	docker := filepath.Join(programs, "Docker Desktop.exe")
	if err := os.WriteFile(docker, binary, 0o755); err != nil {
		t.Fatal(err)
	}
	// Windows scans a program the first time it starts, which a loaded
	// machine can take seconds over, so the stand-in starts once first.
	if output, err := exec.Command(docker, "-test.run=^$").CombinedOutput(); err != nil {
		t.Fatalf("the stand-in Docker Desktop did not run: %v\n%s", err, output)
	}
	pids := filepath.Join(t.TempDir(), "pids")
	root := exec.Command(os.Args[0], "-test.run=^TestKillTreeFixture$")
	root.Env = append(os.Environ(), "CFO_KILL_TREE_ROLE=root", "CFO_KILL_TREE_PIDS="+pids, "CFO_KILL_TREE_SERVICE="+docker, "CFO_KILL_TREE_BINARY="+os.Args[0])
	root.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP, HideWindow: true}
	if err := root.Start(); err != nil {
		t.Fatal(err)
	}
	rootEnded := make(chan struct{})
	go func() { _ = root.Wait(); close(rootEnded) }()
	started := map[string]int{}
	t.Cleanup(func() {
		for _, pid := range started {
			if handle, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, uint32(pid)); err == nil {
				_ = windows.TerminateProcess(handle, 1)
				_, _ = windows.WaitForSingleObject(handle, 10000)
				_ = windows.CloseHandle(handle)
			}
		}
		_ = root.Process.Kill()
		<-rootEnded
	})
	for deadline := time.Now().Add(30 * time.Second); len(started) < 3; time.Sleep(50 * time.Millisecond) {
		if data, err := os.ReadFile(pids); err == nil {
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				if name, value, ok := strings.Cut(line, " "); ok {
					started[name], _ = strconv.Atoi(value)
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the fixture reported %v, want its service, own child and backend", started)
		}
	}

	// Act
	err = killTree(context.Background(), execx.OSRunner{}, root.Process.Pid)

	// Assert
	if err != nil {
		t.Errorf("killTree: %v", err)
	}
	select {
	case <-rootEnded:
	case <-time.After(15 * time.Second):
		t.Error("the process named still runs")
	}
	if killTreeRunning(started["own"]) {
		t.Errorf("its own child, pid %d, still runs", started["own"])
	}
	time.Sleep(time.Second)
	for _, name := range []string{"service", "backend"} {
		if !killTreeRunning(started[name]) {
			t.Errorf("the stand-in Docker Desktop's %s process, pid %d, ended with the tree", name, started[name])
		}
	}
}

// killTreeRunning reports whether pid still runs, waiting up to two seconds
// for it to end.
func killTreeRunning(pid int) bool {
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)
	event, _ := windows.WaitForSingleObject(handle, 2000)
	return event == uint32(windows.WAIT_TIMEOUT)
}
