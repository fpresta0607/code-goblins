//go:build linux || darwin

// Package pty runs one process in a pseudo terminal, the terminal a goblin's
// harness sees on Linux and macOS, and hands its caller the raw byte stream
// both ways, as internal/conpty does with a pseudo console on Windows.
package pty

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

// Spec is one process to run in a pseudo terminal.
type Spec struct {
	// Args is the command and its arguments. Args[0] is found as exec.Command
	// finds a program, on this process's PATH, so a program that only Env's
	// PATH finds is resolved by the caller before Start.
	Args []string
	Dir  string
	// Env is the whole environment, one KEY=value each; nil inherits this
	// process's.
	Env        []string
	Cols, Rows int
}

// Terminal is one running pseudo terminal and the process in it. The process
// leads a session of its own, whose controlling terminal is this one, and it
// starts as a tree (execx.StartTree), so Close ends it and everything it
// started but a machine service, as a pseudo console's job does on Windows.
// Its output ends once the process has exited and what it wrote is read, as a
// pseudo console's does, even while a helper still holds the terminal.
type Terminal struct {
	ptmx  *os.File
	tree  *execx.Tree
	pid   int
	start time.Time
	done  chan struct{}
	code  uint32
}

// Start runs spec.Args in a new pseudo terminal of spec.Cols by spec.Rows.
func Start(spec Spec) (*Terminal, error) {
	if len(spec.Args) == 0 {
		return nil, errors.New("pty: no command to run")
	}
	if err := validSize(spec.Cols, spec.Rows); err != nil {
		return nil, err
	}
	fd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("pty: open /dev/ptmx: %w", err)
	}
	name, err := unlock(fd)
	if err != nil {
		unix.Close(fd)
		return nil, err
	}
	ptmx := os.NewFile(uintptr(fd), "/dev/ptmx")
	// macOS refuses to size the terminal until its device has been opened.
	tty, err := os.OpenFile(name, os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		ptmx.Close()
		return nil, fmt.Errorf("pty: open %s: %w", name, err)
	}
	// The process holds the terminal from here, so once it and everything it
	// starts have closed it, Linux reads the terminal as an input/output error.
	defer tty.Close()
	if err := resize(ptmx, spec.Cols, spec.Rows); err != nil {
		ptmx.Close()
		return nil, err
	}
	cmd := execx.Command(spec.Args[0], spec.Args[1:]...)
	cmd.Dir = spec.Dir
	cmd.Env = spec.Env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	// Setctty makes its standard input, the terminal, the controlling
	// terminal of the session Setsid starts.
	cmd.SysProcAttr.Setsid = true
	cmd.SysProcAttr.Setctty = true
	tree, err := execx.StartTree(cmd)
	if err != nil {
		ptmx.Close()
		return nil, fmt.Errorf("pty: start %s: %w", spec.Args[0], err)
	}
	// Nothing has waited for the process yet, so even one that has exited
	// still has its start to read.
	start, isRead := proc.StartTime(cmd.Process.Pid)
	if !isRead {
		_, _ = tree.End()
		_ = cmd.Wait()
		ptmx.Close()
		return nil, fmt.Errorf("pty: read when %s started", spec.Args[0])
	}
	terminal := &Terminal{ptmx: ptmx, tree: tree, pid: cmd.Process.Pid, start: start, done: make(chan struct{})}
	go terminal.wait(cmd)
	return terminal, nil
}

// wait records the process's exit code once it exits, closes Done and sets a
// read deadline that has passed, so a Read waiting for output, and each Read
// after it, drains what is left.
func (t *Terminal) wait(cmd *exec.Cmd) {
	_ = cmd.Wait()
	t.code = exitCode(cmd.ProcessState)
	close(t.done)
	_ = t.ptmx.SetReadDeadline(time.Now())
}

// exitCode is a process's exit status, or 128 and the signal that ended it,
// as a shell reports one.
func exitCode(state *os.ProcessState) uint32 {
	if status, isWaitStatus := state.Sys().(syscall.WaitStatus); isWaitStatus && status.Signaled() {
		return 128 + uint32(status.Signal())
	}
	return uint32(state.ExitCode())
}

// PID is the process the terminal was started with.
func (t *Terminal) PID() int {
	return t.pid
}

// StartTime is when the process started (proc.StartTime), read before
// anything waited for it, so a process that exits at once still has one.
func (t *Terminal) StartTime() time.Time {
	return t.start
}

// Read reads what the process wrote to its terminal, escape sequences and
// all. Once the process has exited and what is buffered is read, or every
// process holding the terminal has closed it, Read returns io.EOF, so a
// helper that still holds the terminal does not hold its output open.
func (t *Terminal) Read(p []byte) (int, error) {
	n, err := t.ptmx.Read(p)
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return t.drain(p)
	}
	// Linux reads a terminal no process holds as an input/output error.
	if errors.Is(err, syscall.EIO) {
		return n, io.EOF
	}
	return n, err
}

// drain reads what is buffered once the process has exited, without waiting
// for more, and returns io.EOF once nothing is left. Linux moves output still
// on its way to the terminal into the buffer before it reports none.
func (t *Terminal) drain(p []byte) (int, error) {
	raw, err := t.ptmx.SyscallConn()
	if err != nil {
		return 0, err
	}
	var n int
	var readErr error
	if err := raw.Control(func(fd uintptr) {
		n, readErr = unix.Read(int(fd), p)
	}); err != nil {
		return 0, err
	}
	if readErr != nil && !errors.Is(readErr, unix.EAGAIN) && !errors.Is(readErr, unix.EIO) {
		return 0, readErr
	}
	if readErr != nil || n == 0 {
		return 0, io.EOF
	}
	return n, nil
}

// Write types p into the terminal as the process's input.
func (t *Terminal) Write(p []byte) (int, error) {
	return t.ptmx.Write(p)
}

// Resize changes the terminal's size in character cells, which the process
// is told of with SIGWINCH.
func (t *Terminal) Resize(cols, rows int) error {
	if err := validSize(cols, rows); err != nil {
		return err
	}
	return resize(t.ptmx, cols, rows)
}

func resize(ptmx *os.File, cols, rows int) error {
	raw, err := ptmx.SyscallConn()
	if err != nil {
		return fmt.Errorf("pty: resize: %w", err)
	}
	var ioctlErr error
	if err := raw.Control(func(fd uintptr) {
		ioctlErr = unix.IoctlSetWinsize(int(fd), unix.TIOCSWINSZ, &unix.Winsize{Col: uint16(cols), Row: uint16(rows)})
	}); err != nil {
		return fmt.Errorf("pty: resize: %w", err)
	}
	if ioctlErr != nil {
		return fmt.Errorf("pty: resize to %dx%d: %w", cols, rows, ioctlErr)
	}
	return nil
}

// Done is closed once the process has exited. Output it wrote can still be
// buffered then, so the owner reads until Read returns io.EOF, which it does
// once that output is read, whatever helpers still run.
func (t *Terminal) Done() <-chan struct{} {
	return t.done
}

// ExitCode is the process's exit code, once Done is closed.
func (t *Terminal) ExitCode() uint32 {
	<-t.done
	return t.code
}

// Close ends the process and everything it started but a machine service
// (execx.Tree.End), waits for the process to exit and releases the terminal;
// the terminal's owner calls it once.
func (t *Terminal) Close() error {
	_, err := t.tree.End()
	<-t.done
	t.ptmx.Close()
	if err != nil {
		return fmt.Errorf("pty: end the process tree: %w", err)
	}
	return nil
}

func validSize(cols, rows int) error {
	if cols < 2 || rows < 2 || cols > 1000 || rows > 1000 {
		return fmt.Errorf("pty: size %dx%d is outside 2 to 1000 cells", cols, rows)
	}
	return nil
}
