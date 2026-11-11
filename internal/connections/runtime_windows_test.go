package connections

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestConnectionRuntimeChild(t *testing.T) {
	if os.Getenv("CONNECTION_RUNTIME_FIXTURE") != "1" {
		return
	}
	// A byte on stdout tells the test this process has finished starting.
	_, _ = os.Stdout.Write([]byte{1})
	var input [1]byte
	_, _ = os.Stdin.Read(input[:])
	os.Exit(0)
}

func TestConnectionRuntimeRequiresMatchingOwnerFolderAndGeneration(t *testing.T) {
	directory := t.TempDir()
	stateDir := t.TempDir()
	command := execx.Command(os.Args[0], "-test.run=^TestConnectionRuntimeChild$")
	command.Dir = directory
	command.Env = append(os.Environ(), "CONNECTION_RUNTIME_FIXTURE=1", "GOBLINS_SPAWN_GEN=proof", "CONNECTION_FIXTURE_TOKEN=synthetic")
	command.Stderr = io.Discard
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = input.Close(); _ = command.Wait() }()
	// Windows can refuse a read of a process's environment in its first
	// milliseconds, so the checks below wait until the child has started.
	var started [1]byte
	if _, err := io.ReadFull(output, started[:]); err != nil {
		t.Fatalf("the fixture process did not start: %v", err)
	}
	record := host.Record{ID: "proof", Pipe: "scratch", Token: "synthetic", HostPID: os.Getpid(), ChildPID: command.Process.Pid, Started: time.Now().UTC()}
	data, _ := json.Marshal(record)
	if err := os.MkdirAll(filepath.Join(stateDir, "hosts"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "hosts", "proof.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	meta := state.TaskMeta{ID: "proof", SpawnGen: "proof", Backend: "native", Worktree: directory}
	observed, err := nativeRuntime(context.Background(), stateDir, meta)
	if err != nil || len(observed.Args) == 0 || !slices.Contains(observed.Env, "CONNECTION_FIXTURE_TOKEN=synthetic") {
		t.Fatalf("matching runtime unavailable: %v", err)
	}
	if observed.Identity.ProcessID != command.Process.Pid || observed.Identity.StartedAt.IsZero() || observed.Identity.Workspace != directory {
		t.Fatalf("lost proven process identity: %+v", observed.Identity)
	}
	meta.SpawnGen = "replacement"
	if _, err := nativeRuntime(context.Background(), stateDir, meta); err == nil {
		t.Fatal("accepted another generation")
	}
	meta.SpawnGen = "proof"
	meta.Worktree = stateDir
	if _, err := nativeRuntime(context.Background(), stateDir, meta); err == nil {
		t.Fatal("accepted another working folder")
	}
	meta.Worktree = directory
	record.HostPID = command.Process.Pid
	data, _ = json.Marshal(record)
	if err := os.WriteFile(filepath.Join(stateDir, "hosts", "proof.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := nativeRuntime(context.Background(), stateDir, meta); err == nil {
		t.Fatal("accepted another process owner")
	}
}
