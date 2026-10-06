// Package conpty runs one process in a Windows pseudo console, the terminal a
// goblin's harness sees, and hands its caller the raw byte stream both ways.
package conpty

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Spec is one process to run in a pseudo console.
type Spec struct {
	// Args is the command and its arguments, quoted into one Windows command
	// line. Args[0] is found the way CreateProcess finds a program, so give an
	// absolute path to an executable. A command that needs PATH lookup through
	// Env, or a script shim such as an npm .cmd, is resolved by the caller
	// before Start.
	Args []string
	Dir  string
	// Env is the whole environment, one KEY=value each; nil inherits this
	// process's.
	Env        []string
	Cols, Rows int
}

// Console is one running pseudo console and the process in it. The process
// and everything it starts share a job object, so Close ends them all, except
// a process that asks to break away (a goblin host, a detached serve), which
// leaves the job and outlives Close. The console's input waker runs in the
// job too.
type Console struct {
	pc         windows.Handle
	in         *os.File
	out        *os.File
	process    windows.Handle
	job        windows.Handle
	scheduling *jobScheduling
	pid        int
	done       chan struct{}
	code       uint32

	// waker wakes the process when input written to it stays unread, and
	// typed tells the waker of input written, or of a resize. closing
	// closes when Close begins.
	waker   *waker
	typed   chan struct{}
	closing chan struct{}

	// writing holds each write whole, and with it what writes keep: whether
	// a key event was written as Windows sends it, after which conhost holds
	// an Escape that ends an input, and such an ending held back meanwhile,
	// with the number of the hold that kept it.
	writing                  sync.Mutex
	isWindowsKeyEventWritten bool
	held                     []byte
	holds                    int

	mu     sync.Mutex
	closed bool
}

// Start runs spec.Args in a new pseudo console of spec.Cols by spec.Rows.
func Start(spec Spec) (*Console, error) {
	if len(spec.Args) == 0 {
		return nil, errors.New("conpty: a command is required")
	}
	if err := validSize(spec.Cols, spec.Rows); err != nil {
		return nil, err
	}
	env, err := environmentBlock(spec.Env)
	if err != nil {
		return nil, err
	}
	var dir *uint16
	if spec.Dir != "" {
		if dir, err = windows.UTF16PtrFromString(spec.Dir); err != nil {
			return nil, fmt.Errorf("conpty: directory: %w", err)
		}
	}
	commandLine, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(spec.Args))
	if err != nil {
		return nil, fmt.Errorf("conpty: command line: %w", err)
	}
	if err := interactiveScheduling(windows.CurrentProcess()); err != nil {
		return nil, fmt.Errorf("conpty: host scheduling: %w", err)
	}

	var inRead, inWrite, outRead, outWrite windows.Handle
	if err := windows.CreatePipe(&inRead, &inWrite, nil, 0); err != nil {
		return nil, fmt.Errorf("conpty: input pipe: %w", err)
	}
	if err := windows.CreatePipe(&outRead, &outWrite, nil, 0); err != nil {
		windows.CloseHandle(inRead)
		windows.CloseHandle(inWrite)
		return nil, fmt.Errorf("conpty: output pipe: %w", err)
	}
	c := &Console{
		in:      os.NewFile(uintptr(inWrite), "conpty-input"),
		out:     os.NewFile(uintptr(outRead), "conpty-output"),
		typed:   make(chan struct{}, 1),
		closing: make(chan struct{}),
		done:    make(chan struct{}),
	}
	err = createInteractiveConsole(windows.Coord{X: int16(spec.Cols), Y: int16(spec.Rows)}, inRead, outWrite, &c.pc)
	// The pseudo console holds its own copies of these ends.
	windows.CloseHandle(inRead)
	windows.CloseHandle(outWrite)
	if err != nil {
		c.in.Close()
		c.out.Close()
		return nil, fmt.Errorf("conpty: create pseudo console: %w", err)
	}
	if err := c.startProcess(commandLine, dir, env); err != nil {
		windows.ClosePseudoConsole(c.pc)
		c.in.Close()
		c.out.Close()
		return nil, err
	}
	go c.waker.relay(c.typed, c.done, c.closing)
	go c.wait()
	return c, nil
}

// startProcess starts the process suspended, puts it in a job that ends
// every process in it when the job's last handle closes, starts the console's
// input waker in the same job, and only then lets the process run, so
// nothing it starts escapes the job unless it asks to: a process started with
// CREATE_BREAKAWAY_FROM_JOB leaves it, as the host of a goblin a CFO in this
// terminal launches must, to outlive the terminal.
func (c *Console) startProcess(commandLine, dir, env *uint16) error {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return fmt.Errorf("conpty: create job: %w", err)
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_BREAKAWAY_OK
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		windows.CloseHandle(job)
		return fmt.Errorf("conpty: limit job: %w", err)
	}
	attributes, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		windows.CloseHandle(job)
		return fmt.Errorf("conpty: attribute list: %w", err)
	}
	defer attributes.Delete()
	// The attribute's value is the console handle itself, not its address; the
	// handle is read out as a pointer-sized value rather than converted.
	if err := attributes.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, *(*unsafe.Pointer)(unsafe.Pointer(&c.pc)), unsafe.Sizeof(c.pc)); err != nil {
		windows.CloseHandle(job)
		return fmt.Errorf("conpty: attach pseudo console: %w", err)
	}
	startup := windows.StartupInfoEx{ProcThreadAttributeList: attributes.List()}
	startup.Cb = uint32(unsafe.Sizeof(startup))
	// Without this, a caller whose own standard handles are redirected would
	// hand them to the process instead of the pseudo console.
	startup.Flags = windows.STARTF_USESTDHANDLES
	var info windows.ProcessInformation
	flags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_UNICODE_ENVIRONMENT | windows.CREATE_SUSPENDED)
	if err := windows.CreateProcess(nil, commandLine, nil, nil, false, flags, env, dir, &startup.StartupInfo, &info); err != nil {
		windows.CloseHandle(job)
		return fmt.Errorf("conpty: start process: %w", err)
	}
	defer windows.CloseHandle(info.Thread)
	if err := windows.AssignProcessToJobObject(job, info.Process); err != nil {
		windows.TerminateProcess(info.Process, 1)
		windows.CloseHandle(info.Process)
		windows.CloseHandle(job)
		return fmt.Errorf("conpty: assign job: %w", err)
	}
	if err := interactiveScheduling(info.Process); err != nil {
		windows.TerminateJobObject(job, 1)
		windows.CloseHandle(info.Process)
		windows.CloseHandle(job)
		return fmt.Errorf("conpty: child scheduling: %w", err)
	}
	scheduling, err := monitorJobScheduling(job)
	if err != nil {
		windows.TerminateJobObject(job, 1)
		windows.CloseHandle(info.Process)
		windows.CloseHandle(job)
		return err
	}
	waker, err := startWaker(c.pc, job)
	if err != nil {
		windows.TerminateJobObject(job, 1)
		scheduling.close()
		windows.CloseHandle(info.Process)
		windows.CloseHandle(job)
		return err
	}
	if _, err := windows.ResumeThread(info.Thread); err != nil {
		windows.TerminateJobObject(job, 1)
		scheduling.close()
		waker.typed.Close()
		waker.report.Close()
		windows.CloseHandle(info.Process)
		windows.CloseHandle(job)
		return fmt.Errorf("conpty: resume process: %w", err)
	}
	c.process, c.job, c.pid = info.Process, job, int(info.ProcessId)
	c.scheduling, c.waker = scheduling, waker
	return nil
}

// wait records the process's exit code, then closes the pseudo console so
// Read reaches the end of its output.
func (c *Console) wait() {
	windows.WaitForSingleObject(c.process, windows.INFINITE)
	windows.GetExitCodeProcess(c.process, &c.code)
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	windows.ClosePseudoConsole(c.pc)
	close(c.done)
}

// PID is the process the console was started with.
func (c *Console) PID() int {
	return c.pid
}

// Read reads what the process wrote to its terminal, escape sequences and
// all. It returns io.EOF once the process has exited and its output is read.
func (c *Console) Read(p []byte) (int, error) {
	return c.out.Read(p)
}

// heldEndingWait is how long an ending conhost would hold waits for the next
// write: cfo attach writes what its console reads 4 KiB at a time, so a key
// event can be split across two writes, and the rest follows at once, while
// an Escape typed alone has nothing after it.
const heldEndingWait = 100 * time.Millisecond

// Write types p into the terminal as the process's input, and tells the
// console's input waker, which wakes the process if the input stays unread.
// Once a key event has been written as Windows sends it, an ending conhost
// would hold is held back: written on with the next input when that comes
// within heldEndingWait, as the rest of a split key event does, and
// otherwise as the key events it stands for.
func (c *Console) Write(p []byte) (int, error) {
	if err := c.scheduling.reconcile(); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	c.writing.Lock()
	defer c.writing.Unlock()
	input := p
	if c.held != nil {
		input = append(c.held, p...)
		c.held = nil
	}
	if windowsKeyEvent.Match(input) {
		c.isWindowsKeyEventWritten = true
	}
	if ending := heldEnding(input); c.isWindowsKeyEventWritten && ending > 0 {
		c.held = slices.Clone(input[len(input)-ending:])
		input = input[:len(input)-ending]
		c.holds++
		hold := c.holds
		time.AfterFunc(heldEndingWait, func() { c.release(hold) })
	}
	if len(input) == 0 {
		return len(p), nil
	}
	n, err := c.in.Write(input)
	if n > 0 {
		c.wake()
	}
	if err != nil {
		return min(n, len(p)), err
	}
	return len(p), nil
}

// release writes the ending hold kept back as the key events it stands for,
// unless a write since took it on.
func (c *Console) release(hold int) {
	c.writing.Lock()
	defer c.writing.Unlock()
	if hold != c.holds || c.held == nil {
		return
	}
	keys := asKeyEvents(c.held)
	c.held = nil
	if _, err := c.in.Write(keys); err == nil {
		c.wake()
	}
}

// wake tells the console's input waker that the console has input it did not
// have before, without ever waiting on the waker.
func (c *Console) wake() {
	select {
	case c.typed <- struct{}{}:
	default:
	}
}

// Resize changes the terminal's size in character cells.
func (c *Console) Resize(cols, rows int) error {
	if err := validSize(cols, rows); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errors.New("conpty: the console has closed")
	}
	// A resize reaches the process as an input event, which can be left
	// unread as typed input can.
	if err := windows.ResizePseudoConsole(c.pc, windows.Coord{X: int16(cols), Y: int16(rows)}); err != nil {
		return err
	}
	c.wake()
	return nil
}

// Done is closed once the process has exited and its pseudo console has
// closed. Before Windows 24H2, closing it waits for the output to drain, so
// Done fires only while the owner keeps reading; from 24H2 output can still
// be buffered when it fires. Either way the owner reads until Read returns
// io.EOF, as Close does.
func (c *Console) Done() <-chan struct{} {
	return c.done
}

// ExitCode is the process's exit code, once Done is closed.
func (c *Console) ExitCode() uint32 {
	<-c.done
	return c.code
}

// Close ends the process and everything it started, and releases the
// console; the console's owner calls it once. Closing the pseudo console can
// wait for its output to be read, so the output is drained here too.
func (c *Console) Close() error {
	close(c.closing)
	err := windows.TerminateJobObject(c.job, 1)
	go func() {
		buffer := make([]byte, 32<<10)
		for {
			if _, readErr := c.out.Read(buffer); readErr != nil {
				return
			}
		}
	}()
	<-c.done
	c.in.Close()
	c.out.Close()
	c.scheduling.close()
	windows.CloseHandle(c.process)
	windows.CloseHandle(c.job)
	if err != nil {
		return fmt.Errorf("conpty: end the process tree: %w", err)
	}
	return nil
}

func validSize(cols, rows int) error {
	if cols < 2 || rows < 2 || cols > 1000 || rows > 1000 {
		return fmt.Errorf("conpty: size %dx%d is outside 2 to 1000 cells", cols, rows)
	}
	return nil
}

// environmentBlock renders env as the NUL-separated, doubly NUL-terminated
// UTF-16 block CreateProcess takes, or nil to inherit.
func environmentBlock(env []string) (*uint16, error) {
	if env == nil {
		return nil, nil
	}
	var block []uint16
	for _, entry := range env {
		if !strings.Contains(entry, "=") {
			return nil, fmt.Errorf("conpty: environment entry %q is not KEY=value", entry)
		}
		encoded, err := windows.UTF16FromString(entry)
		if err != nil {
			return nil, err
		}
		block = append(block, encoded...)
	}
	if len(block) == 0 {
		block = append(block, 0)
	}
	block = append(block, 0)
	return &block[0], nil
}
