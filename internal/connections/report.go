package connections

import (
	"context"
	"errors"
	"io"
	"runtime"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/spawn"
)

func harnessReport(ctx context.Context, name string, args []string, dir string, env []string, report func(io.Reader, io.Writer) ([]Entry, error)) ([]Entry, error) {
	program := append([]string{name}, args...)
	if runtime.GOOS == "windows" {
		var err error
		program, err = spawn.NativeProgram(name, args...)
		if err != nil {
			return nil, errors.New("Harness is unavailable for a connection check.")
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	command := execx.CommandContext(ctx, program[0], program[1:]...)
	command.Dir, command.WaitDelay = dir, 2*time.Second
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if key != "CLAUDECODE" && key != "CLAUDE_CODE_ENTRYPOINT" {
			command.Env = append(command.Env, entry)
		}
	}
	input, err := command.StdinPipe()
	if err != nil {
		return nil, errors.New("Connection check could not start.")
	}
	output, err := command.StdoutPipe()
	if err != nil {
		return nil, errors.New("Connection check could not start.")
	}
	command.Stderr = io.Discard
	// The check runs as a tree, so its end ends what the harness started for
	// it as well (execx.StartTree).
	tree, err := execx.StartTree(command)
	if err != nil {
		return nil, errors.New("Connection check could not start.")
	}
	defer func() { _ = input.Close(); cancel(); _ = command.Wait(); _ = tree.Release() }()
	return report(output, input)
}
