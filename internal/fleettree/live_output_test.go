package fleettree

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/standin"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestChildShellKeepsTerminalLayoutAndReadsNewOutput(t *testing.T) {
	home := t.TempDir()
	output := filepath.Join(home, "shell output", "b1.output")
	goblin, _ := claudeGoblin(t, home,
		call("s1", "Bash", object{"command": "go test ./..."}, at),
		result("s1", "Output is being written to: "+output, object{"backgroundTaskId": "b1"}, at))
	reader := Reader{Home: home, Now: func() time.Time { return at }}
	writeFile(t, output, "starting\n\n  indented\n\x1b[32mPASS\x1b[0m\n", at)
	if _, err := reader.Read(context.Background(), goblin); err != nil {
		t.Fatal(err)
	}
	lines, err := reader.ChildLines("tree", "shell:b1")
	if err != nil || !strings.Contains(strings.Join(lines, "\n"), "starting\n\n  indented\n\x1b[32mPASS\x1b[0m\n") {
		t.Fatalf("terminal layout lost: %q, %v", lines, err)
	}
	writeFile(t, output, "starting\n\n  indented\n\x1b[32mPASS\x1b[0m\nnext result", at)
	lines, err = reader.ChildLines("tree", "shell:b1")
	if err != nil || !strings.HasSuffix(strings.Join(lines, "\n"), "next result") {
		t.Fatalf("new output was not read: %q, %v", lines, err)
	}
}

func TestProcessChildReadsInheritedCaptureWithoutAPathInItsCommand(t *testing.T) {
	directory := t.TempDir()
	program := filepath.Join(directory, "output-fixture.exe")
	standin.Put(t, program)
	standin.RemoveAtCleanup(t, directory)
	output := filepath.Join(directory, "output.log")
	file, err := os.Create(output)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { file.Close() })
	command := execx.Command(program, "go", "test", "./...")
	command.Env = append(os.Environ(), standin.Env("", standin.Rule{Any: true, Stdout: "inherited capture\n", Hold: true})...)
	command.Stdout = file
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Process.Kill(); _ = command.Wait() })
	started, ok := proc.StartTime(command.Process.Pid)
	if !ok {
		t.Fatal("cannot read the fixture start time")
	}
	reader := Reader{
		Home: directory, Now: func() time.Time { return at },
		Processes: func() ([]Process, error) {
			return staleHarness(Process{PID: command.Process.Pid, ParentPID: 100, Exe: "output-fixture.exe", Created: 60, Started: started}), nil
		},
		Listeners:   func() (map[int][]int, error) { return nil, nil },
		CommandLine: func(int) (string, error) { return "output-fixture.exe go test ./...", nil },
	}
	var lines []string
	for attempt := 0; attempt < 50; attempt++ {
		_, err = reader.Read(context.Background(), Goblin{Meta: state.TaskMeta{ID: "tree", Harness: "claude"}, HarnessPID: 100})
		if err != nil {
			t.Fatal(err)
		}
		lines, err = reader.ChildLines("tree", fmt.Sprintf("process:%d:60", command.Process.Pid))
		if slices.Contains(lines, "inherited capture") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("did not find inherited stdout: %q, %v", lines, err)
}

func TestProcessChildReadsItsOwnRedirectedOutputForEachHarness(t *testing.T) {
	for _, harness := range []string{"claude", "codex", "pi"} {
		t.Run(harness, func(t *testing.T) {
			home := t.TempDir()
			output := filepath.Join(home, "tasks", "test output.log")
			writeFile(t, output, "=== RUN TestStream\n", at)
			started := at.Add(-time.Minute)
			goblin := Goblin{Meta: state.TaskMeta{ID: "tree", Harness: harness}, HarnessPID: 100}
			reader := Reader{
				Home: home, Now: func() time.Time { return at },
				Processes: func() ([]Process, error) {
					return []Process{
						{PID: 100, Exe: harness + ".exe", Started: started.Add(-time.Hour)},
						{PID: 600, ParentPID: 100, Exe: "bash.exe", Created: 60, Started: started, CPU: 10 * time.Second},
					}, nil
				},
				Listeners:   func() (map[int][]int, error) { return nil, nil },
				CommandLine: func(pid int) (string, error) { return `bash -c "go test ./..."`, nil },
				OutputFile:  func(pid int, started time.Time) (string, error) { return output, nil },
			}
			if _, err := reader.Read(context.Background(), goblin); err != nil {
				t.Fatal(err)
			}
			lines, err := reader.ChildLines("tree", "process:600:60")
			if err != nil || !strings.Contains(strings.Join(lines, "\n"), "=== RUN TestStream") || strings.Contains(strings.Join(lines, "\n"), "(pid ") {
				t.Fatalf("not the child's output: %q, %v", lines, err)
			}
			writeFile(t, output, "=== RUN TestStream\n--- PASS: TestStream\n", at)
			lines, err = reader.ChildLines("tree", "process:600:60")
			if err != nil || !slices.Contains(lines, "--- PASS: TestStream") {
				t.Fatalf("not following output: %q, %v", lines, err)
			}
		})
	}
}

func TestShellUsesItsProcessCaptureWhenTheHarnessNamesNone(t *testing.T) {
	home := t.TempDir()
	output := filepath.Join(home, "test.log")
	writeFile(t, output, "actual shell output", at)
	goblin, _ := claudeGoblin(t, home,
		call("s1", "Bash", object{"command": "go test ./..."}, at.Add(-time.Minute)),
		result("s1", "Running in the background", object{"backgroundTaskId": "b1"}, at.Add(-time.Minute)))
	goblin.HarnessPID = 100
	reader := Reader{
		Home: home, Now: func() time.Time { return at },
		Processes: func() ([]Process, error) {
			return staleHarness(Process{PID: 600, ParentPID: 100, Exe: "bash.exe", Created: 60, Started: at.Add(-time.Minute)}), nil
		},
		Listeners:   func() (map[int][]int, error) { return nil, nil },
		CommandLine: func(int) (string, error) { return `bash.exe -c "eval 'go test ./...'"`, nil },
		OutputFile:  func(int, time.Time) (string, error) { return output, nil },
	}
	if _, err := reader.Read(context.Background(), goblin); err != nil {
		t.Fatal(err)
	}
	lines, err := reader.ChildLines("tree", "shell:b1")
	if err != nil || !slices.Contains(lines, "actual shell output") {
		t.Fatalf("shell lost its capture: %q, %v", lines, err)
	}
}
