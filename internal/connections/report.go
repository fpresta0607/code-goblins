package connections

import (
	"context"
	"errors"
	"io"
	"runtime"
	"strconv"
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
	if runtime.GOOS == "windows" {
		command.Cancel = func() error {
			return execx.Command("taskkill", "/PID", strconv.Itoa(command.Process.Pid), "/T", "/F").Run()
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
	if err := command.Start(); err != nil {
		return nil, errors.New("Connection check could not start.")
	}
	defer func() { _ = input.Close(); cancel(); _ = command.Wait() }()
	return report(output, input)
}
