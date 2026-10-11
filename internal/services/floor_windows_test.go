package services

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

// fakeDockerFolder names, to this test program run as docker, the folder it
// keeps its record in: every call it answers, and the processes its compose
// up started.
const fakeDockerFolder = "SERVICES_FAKE_DOCKER"

// TestFakeDockerProgram is docker for TestABuildUnderTheFloorEndsOnlyItsOwnProcesses
// when this test program is run with fakeDockerFolder set, and does nothing
// otherwise. Its engine runs with no container on it, and its compose up
// starts a build of its own, a process that starts another, both waiting
// until they are ended or twenty seconds pass.
func TestFakeDockerProgram(t *testing.T) {
	folder := os.Getenv(fakeDockerFolder)
	if folder == "" {
		return
	}
	arguments := os.Args[len(os.Args)-countAfterDashes():]
	call := strings.Join(arguments, " ")
	calls, err := fsx.OpenAppend(filepath.Join(folder, "calls"), 0o600)
	if err != nil {
		os.Exit(9)
	}
	fmt.Fprintln(calls, call)
	_ = calls.Close()
	switch {
	case call == "info --format {{.ServerVersion}}":
		fmt.Println("29.7.2")
	case strings.HasPrefix(call, "compose ") && strings.Contains(call, " up "):
		child := execx.Command(os.Args[0], "-test.run=^TestFakeDockerProgram$", "--", "build-step")
		child.Env = os.Environ()
		if err := child.Start(); err != nil {
			os.Exit(9)
		}
		pids := fmt.Sprintf("%d %d", os.Getpid(), child.Process.Pid)
		if err := os.WriteFile(filepath.Join(folder, "build"), []byte(pids), 0o600); err != nil {
			os.Exit(9)
		}
		time.Sleep(20 * time.Second)
	case call == "build-step" || call == "sibling":
		time.Sleep(20 * time.Second)
	}
	os.Exit(0)
}

// countAfterDashes counts this program's arguments after its first --.
func countAfterDashes() int {
	for index, argument := range os.Args {
		if argument == "--" {
			return len(os.Args) - index - 1
		}
	}
	return 0
}

// fakeDockerRunner runs docker as this test program and nothing else: a call
// to any other program fails, so no real docker or wsl.exe is ever run.
type fakeDockerRunner struct {
	folder string
}

func (r fakeDockerRunner) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	if request.Name != "docker" {
		return execx.Result{}, fmt.Errorf("the test runs no %s", request.Name)
	}
	request.Args = append([]string{"-test.run=^TestFakeDockerProgram$", "--"}, request.Args...)
	request.Name = os.Args[0]
	request.Env = append(os.Environ(), fakeDockerFolder+"="+r.folder)
	return execx.OSRunner{}.Run(ctx, request)
}

// started is one process by its pid and its start, so a pid used again by
// another process is never taken for it.
type started struct {
	pid   int
	start time.Time
}

func startedNow(t *testing.T, pid int) started {
	t.Helper()
	start, isRunning := proc.StartTime(pid)
	if !isRunning {
		t.Fatalf("process %d is not running", pid)
	}
	return started{pid: pid, start: start}
}

func (s started) isRunning() bool {
	start, isRunning := proc.StartTime(s.pid)
	return isRunning && start.Equal(s.start)
}

// A build that takes the machine under the floor is ended with everything
// it started, and nothing else: compose up's process and the build step it
// started end, and a process beside them that the build did not start runs
// on. The start then takes down the stack's own containers through compose
// and nothing more, and leaves the engine, which it did not start, running.
func TestABuildUnderTheFloorEndsOnlyItsOwnProcesses(t *testing.T) {
	// Arrange
	folder := t.TempDir()
	dataDir := t.TempDir()
	writeManifest(t, dataDir, "PrecisionDocs-AI", testManifest)
	checkout := filepath.Join(t.TempDir(), "PrecisionDocs-AI")
	if err := os.MkdirAll(checkout, 0o755); err != nil {
		t.Fatal(err)
	}
	sibling := execx.Command(os.Args[0], "-test.run=^TestFakeDockerProgram$", "--", "sibling")
	sibling.Env = append(os.Environ(), fakeDockerFolder+"="+folder)
	if err := sibling.Start(); err != nil {
		t.Fatal(err)
	}
	beside := startedNow(t, sibling.Process.Pid)
	var build []started
	t.Cleanup(func() {
		for _, each := range append(build, beside) {
			if each.isRunning() {
				_ = proc.TerminateVerified(each.pid, each.start, func([]string) error { return nil })
			}
		}
		_ = sibling.Wait()
	})
	// Memory is plenty until the build has started, and under the floor from
	// then on.
	var isBuilding atomic.Bool
	memory := func() (Memory, error) {
		if !isBuilding.Load() {
			if pids, err := fsx.ReadFile(filepath.Join(folder, "build")); err == nil {
				for _, field := range strings.Fields(string(pids)) {
					pid, err := strconv.Atoi(field)
					if err != nil {
						return Memory{}, err
					}
					if start, isRunning := proc.StartTime(pid); isRunning {
						build = append(build, started{pid: pid, start: start})
					}
				}
				isBuilding.Store(true)
			}
		}
		if isBuilding.Load() {
			return Memory{Available: 3 * gigabyte, CommitAvailable: 12 * gigabyte}, nil
		}
		return Memory{Available: 12 * gigabyte, CommitAvailable: 16 * gigabyte}, nil
	}
	var said bytes.Buffer
	service := Service{
		StateDir:    t.TempDir(),
		DataDir:     dataDir,
		Docker:      CLI{Commands: fakeDockerRunner{folder: folder}, Out: &said},
		Commands:    &fakeCheck{},
		Memory:      memory,
		Floor:       4 * gigabyte,
		IsLive:      func(task string) bool { return task == "task-a" },
		Now:         time.Now,
		Sleep:       time.Sleep,
		Poll:        time.Second,
		Out:         &said,
		Turn:        func(context.Context, string) (func(), error) { return func() {}, nil },
		FloorEvery:  20 * time.Millisecond,
		ReliefEvery: time.Hour,
	}

	// Act
	_, err := service.Up(context.Background(), checkout, "task-a", 0)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "the 4 GB floor ended it") {
		t.Fatalf("err = %v, want the floor to end the start; said %q", err, said.String())
	}
	if len(build) != 2 {
		t.Fatalf("the build started %v, want compose up and its build step", build)
	}
	for deadline := time.Now().Add(10 * time.Second); (build[0].isRunning() || build[1].isRunning()) && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
	}
	for index, each := range build {
		if each.isRunning() {
			t.Errorf("build process %d (pid %d) still runs, want everything the build started ended", index, each.pid)
		}
	}
	if !beside.isRunning() {
		t.Errorf("the process beside the build (pid %d) was ended, want it running", beside.pid)
	}
	calls, readErr := fsx.ReadFile(filepath.Join(folder, "calls"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	var after []string
	isAfterUp := false
	for _, call := range strings.Split(strings.TrimSpace(string(calls)), "\n") {
		call = strings.TrimSpace(call)
		if isAfterUp && call != "info --format {{.ServerVersion}}" && call != "sibling" && call != "build-step" {
			after = append(after, call)
		}
		isAfterUp = isAfterUp || strings.Contains(call, " up ")
	}
	want := "compose --file docker-compose.dev.yml --env-file .env.docker.local down"
	if len(after) != 1 || after[0] != want {
		t.Errorf("after the build docker was asked %q, want only %q", after, want)
	}
	if errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want the floor named, not a bare cancel", err)
	}
}
