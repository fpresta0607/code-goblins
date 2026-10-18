package proc_test

import (
	"bufio"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/standin"
)

func TestOutputFileNeverConsumesAPipeOrFollowsAReusedPID(t *testing.T) {
	directory := t.TempDir()
	program := filepath.Join(directory, "output-fixture.exe")
	standin.Put(t, program)
	standin.RemoveAtCleanup(t, directory)
	command := execx.Command(program)
	command.Env = append(os.Environ(), standin.Env("", standin.Rule{Any: true, Stdout: "still here\n", Hold: true})...)
	pipe, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Process.Kill(); _ = command.Wait() })
	started, ok := proc.StartTime(command.Process.Pid)
	if !ok {
		t.Fatal("fixture start time unavailable")
	}
	if path, err := proc.OutputFile(command.Process.Pid, started); err != nil || path != "" {
		t.Fatalf("pipe treated as capture: %q, %v", path, err)
	}
	if path, err := proc.OutputFile(command.Process.Pid, started.Add(-time.Second)); err == nil || path != "" {
		t.Fatalf("followed a reused pid: %q, %v", path, err)
	}
	read := make(chan string, 1)
	go func() { line, _ := bufio.NewReader(pipe).ReadString('\n'); read <- line }()
	select {
	case line := <-read:
		if line != "still here\n" {
			t.Fatalf("pipe was consumed: %q", line)
		}
	case <-time.After(time.Minute):
		t.Fatal("pipe output unavailable")
	}
}
