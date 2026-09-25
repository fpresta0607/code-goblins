// Package conpty runs one process in a Windows pseudo console, the terminal a
// goblin's harness sees, and hands its caller the raw byte stream both ways.
package conpty

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
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
// and everything it starts share a job object, so Close ends the whole tree.
type Console struct {
	pc      windows.Handle
	in      *os.File
	out     *os.File
	process windows.Handle
	job     windows.Handle
	pid     int
	done    chan struct{}
	code    uint32

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
		in:   os.NewFile(uintptr(inWrite), "conpty-input"),
		out:  os.NewFile(uintptr(outRead), "conpty-output"),
		done: make(chan struct{}),
	}
	err = windows.CreatePseudoConsole(windows.Coord{X: int16(spec.Cols), Y: int16(spec.Rows)}, inRead, outWrite, 0, &c.pc)
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
	go c.wait()
	return c, nil
}

// startProcess starts the process suspended, puts it in a job that ends
// every process in it when the job's last handle closes, and only then lets
// it run, so nothing it starts can escape the job.
func (c *Console) startProcess(commandLine, dir, env *uint16) error {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return fmt.Errorf("conpty: create job: %w", err)
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
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
	if _, err := windows.ResumeThread(info.Thread); err != nil {
		windows.TerminateJobObject(job, 1)
		windows.CloseHandle(info.Process)
		windows.CloseHandle(job)
		return fmt.Errorf("conpty: resume process: %w", err)
	}
	c.process, c.job, c.pid = info.Process, job, int(info.ProcessId)
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

// Write types p into the terminal as the process's input.
func (c *Console) Write(p []byte) (int, error) {
	return c.in.Write(p)
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
	return windows.ResizePseudoConsole(c.pc, windows.Coord{X: int16(cols), Y: int16(rows)})
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
