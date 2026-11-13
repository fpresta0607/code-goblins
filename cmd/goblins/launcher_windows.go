package main

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

// startDetachedAfter is startDetached for a process that must find something
// in place before its first instruction runs: it is created suspended, before
// runs with its pid, and only then is it let run. A before that fails ends
// the process, which never ran. It starts with environment, or with this
// process's own when environment is nil.
func startDetachedAfter(before func(pid int) error, environment []string, executable, dir, logPath string, args ...string) (*exec.Cmd, error) {
	log, err := fsx.OpenAppend(logPath, 0o600)
	if err != nil {
		return nil, err
	}
	defer log.Close()
	var command *exec.Cmd
	for _, flags := range []uint32{createNoWindow | createNewProcessGroup | createBreakawayFromJob, createNoWindow | createNewProcessGroup} {
		command = execx.Command(executable, args...)
		command.Dir = dir
		command.Env = environment
		command.Stdout, command.Stderr = log, log
		command.SysProcAttr.CreationFlags |= flags
		command.SysProcAttr.HideWindow = true
		if before != nil {
			command.SysProcAttr.CreationFlags |= createSuspended
		}
		if err = command.Start(); err != nil {
			continue
		}
		if before == nil {
			return command, nil
		}
		if err = before(command.Process.Pid); err == nil {
			err = proc.Resume(command.Process.Pid)
		}
		if err != nil {
			_ = command.Process.Kill()
			_ = command.Wait()
			return nil, err
		}
		return command, nil
	}
	return nil, err
}

// openWindow starts the desktop window beside this binary on board, in the
// tray alone with background. A window already running takes the launch as
// its second instance and comes to the front. Its output goes to
// state/window.log, and it outlives this terminal.
func openWindow(board, stateDir string, background bool) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	window := filepath.Join(filepath.Dir(self), windowProgram)
	if _, err := os.Stat(window); errors.Is(err, fs.ErrNotExist) {
		return errNoWindow
	}
	args := []string{"--board", board, "--state", stateDir}
	if background {
		args = append(args, "--background")
	}
	log, err := fsx.OpenAppend(filepath.Join(stateDir, "window.log"), 0o600)
	if err != nil {
		return err
	}
	defer log.Close()
	// HideWindow stays unset, unlike the supervisor's start: it would hide
	// the window's first show as well.
	for _, flags := range []uint32{createNewProcessGroup | createBreakawayFromJob, createNewProcessGroup} {
		command := execx.Command(window, args...)
		command.Dir = filepath.Dir(self)
		command.Env = windowEnvironment(os.Environ(), self)
		command.Stdout, command.Stderr = log, log
		command.SysProcAttr.CreationFlags |= flags
		if err = command.Start(); err == nil {
			return command.Process.Release()
		}
	}
	return err
}
