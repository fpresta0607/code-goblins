package proc

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// The read must agree with the operating system on this process, which is the
// only working directory a test can know independently.
func TestWorkingDirectoryReadsThisProcess(t *testing.T) {
	want, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	got, err := WorkingDirectory(Self())
	if err != nil {
		t.Fatalf("WorkingDirectory(self): %v", err)
	}
	if !sameDir(got, want) {
		t.Errorf("WorkingDirectory(self) = %q, want %q", got, want)
	}
}

// The whole reason this exists: a process started from a directory carries no
// path in its command line, so the directory is invisible to every process
// listing. A dev server left running in a retired worktree is exactly this
// shape, and it is the leak the runtime report is built to find.
func TestWorkingDirectoryFindsADirectoryNoCommandLineNames(t *testing.T) {
	dir := t.TempDir()
	command := exec.Command("cmd.exe", "/c", "pause")
	command.Dir = dir
	if err := command.Start(); err != nil {
		t.Skipf("could not start a child process: %v", err)
	}
	t.Cleanup(func() {
		_ = command.Process.Kill()
		_, _ = command.Process.Wait()
	})

	// The command line names cmd.exe and "pause" and nothing else; only the
	// parameter block knows where it is running.
	if strings.Contains(strings.Join(command.Args, " "), dir) {
		t.Fatalf("the fixture's own command line names the directory, so it proves nothing")
	}

	var got string
	var err error
	for attempt := 0; attempt < 50; attempt++ {
		got, err = WorkingDirectory(command.Process.Pid)
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("WorkingDirectory(child): %v", err)
	}
	if !sameDir(got, dir) {
		t.Errorf("WorkingDirectory(child) = %q, want %q", got, dir)
	}
}

// A process this one cannot open must report a plain failure rather than an
// empty string that would read as "started from the drive root". PID 4 is the
// System process, which no unprivileged caller can open.
func TestWorkingDirectoryRefusesAProcessItCannotOpen(t *testing.T) {
	got, err := WorkingDirectory(4)
	if err == nil {
		t.Skipf("this session can open the System process (%q); the refusal path is untestable here", got)
	}
	if !errors.Is(err, ErrDirectoryUnreadable) {
		t.Errorf("err = %v, want it to wrap ErrDirectoryUnreadable", err)
	}
	if got != "" {
		t.Errorf("directory = %q, want nothing alongside an error", got)
	}
}

func TestWorkingDirectoryRefusesAPIDThatIsNotRunning(t *testing.T) {
	// A pid far above the live range: the read must fail, never guess.
	if got, err := WorkingDirectory(0x7FFFFFF0); err == nil {
		t.Errorf("WorkingDirectory(unused pid) = %q, want an error", got)
	}
}

// A quoted argument comes back whole: the split is the one the program
// itself read its command line by, not a split on spaces.
func TestArgumentsSplitsACommandLineTheWayTheProgramReadIt(t *testing.T) {
	command := exec.Command("cmd.exe", "/d", "/k", "rem", "two words")
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Skipf("could not start a child process: %v", err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = command.Process.Kill()
		_, _ = command.Process.Wait()
	})

	var args []string
	for attempt := 0; attempt < 50; attempt++ {
		if args, err = Arguments(command.Process.Pid); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("Arguments(child): %v", err)
	}
	want := []string{"/d", "/k", "rem", "two words"}
	if len(args) != len(want)+1 || !reflect.DeepEqual(args[1:], want) {
		t.Errorf("Arguments(child) = %q, want the program and then %q", args, want)
	}
}

// One read of the parameter block must return what the two separate reads do.
func TestParametersReadsTheDirectoryAndArgumentsTogether(t *testing.T) {
	dir := t.TempDir()
	command := exec.Command("cmd.exe", "/d", "/k", "rem", "two words")
	command.Dir = dir
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatalf("could not start a child process: %v", err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = command.Process.Kill()
		_, _ = command.Process.Wait()
	})

	var directory string
	var args []string
	for attempt := 0; attempt < 50; attempt++ {
		if directory, args, err = Parameters(command.Process.Pid); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("Parameters(child): %v", err)
	}
	if !sameDir(directory, dir) {
		t.Errorf("directory = %q, want %q", directory, dir)
	}
	want := []string{"/d", "/k", "rem", "two words"}
	if len(args) != len(want)+1 || !reflect.DeepEqual(args[1:], want) {
		t.Errorf("arguments = %q, want the program and then %q", args, want)
	}
}

func TestParametersRefusesAPIDThatIsNotRunning(t *testing.T) {
	directory, args, err := Parameters(0x7FFFFFF0)
	if !errors.Is(err, ErrDirectoryUnreadable) || !errors.Is(err, ErrCommandLineUnreadable) {
		t.Errorf("err = %v, want both values reported unreadable", err)
	}
	if directory != "" || args != nil {
		t.Errorf("Parameters(unused pid) = %q %q, want nothing alongside an error", directory, args)
	}
}

func TestParametersReadsArgumentsBeyondTheFirstParameterPage(t *testing.T) {
	directory := t.TempDir()
	argument := strings.Repeat("two words ", 400)
	command := exec.Command(os.Args[0], "-test.run=^TestParametersLongArgumentFixture$", argument)
	command.Dir = directory
	command.Env = append(os.Environ(), "CFO_PARAMETER_FIXTURE=1")
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Process.Kill(); _ = command.Wait() })
	gotDirectory, arguments, err := Parameters(command.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if !sameDir(gotDirectory, directory) || len(arguments) != 3 || arguments[2] != argument {
		t.Fatalf("directory or long argument changed: directory=%q argument count=%d", gotDirectory, len(arguments))
	}
}

func TestParametersLongArgumentFixture(t *testing.T) {
	if os.Getenv("CFO_PARAMETER_FIXTURE") == "1" {
		fmt.Println("fixture running")
		time.Sleep(time.Minute)
	}
}

// Every process moves its environment and then its parameter block into its
// own heap as it starts, frees the block CreateProcess built, and can put
// something else at that address at once, so a read that follows a pointer
// taken before the move can find a partial copy ("Only part of a
// ReadProcessMemory or WriteProcessMemory request was completed"), zeros or
// another allocation's bytes. The fleet reads a goblin's process the moment
// it starts. Each reader reads a child first while it is suspended, before
// any move, then from its release until its own code runs, after the move,
// and reads the child's true values every time.
func TestReadersReadAProcessThroughItsStartup(t *testing.T) {
	argument := strings.Repeat("two words ", 400)
	wantDirectory := func(got, want string) error {
		if !sameDir(got, want) {
			return fmt.Errorf("directory = %q, want %q", got, want)
		}
		return nil
	}
	wantArguments := func(got []string) error {
		if len(got) != 3 || got[2] != argument {
			return fmt.Errorf("read %d arguments, want the program, the test filter and the long argument", len(got))
		}
		return nil
	}
	wantEnvironment := func(got []string) error {
		if !slices.Contains(got, "CFO_PARAMETER_FIXTURE=1") {
			return fmt.Errorf("an environment of %d entries without CFO_PARAMETER_FIXTURE=1", len(got))
		}
		return nil
	}
	readers := []struct {
		name string
		read func(pid int, start time.Time, directory string) error
	}{
		{"Parameters", func(pid int, _ time.Time, directory string) error {
			got, arguments, err := Parameters(pid)
			if err != nil {
				return err
			}
			return errors.Join(wantDirectory(got, directory), wantArguments(arguments))
		}},
		{"WorkingDirectory", func(pid int, _ time.Time, directory string) error {
			got, err := WorkingDirectory(pid)
			if err != nil {
				return err
			}
			return wantDirectory(got, directory)
		}},
		{"Arguments", func(pid int, _ time.Time, _ string) error {
			arguments, err := Arguments(pid)
			if err != nil {
				return err
			}
			return wantArguments(arguments)
		}},
		{"Environment", func(pid int, _ time.Time, _ string) error {
			environment, err := Environment(pid)
			if err != nil {
				return err
			}
			return wantEnvironment(environment)
		}},
		{"Identify", func(pid int, start time.Time, directory string) error {
			identity, err := Identify(pid, start)
			if err != nil {
				return err
			}
			return errors.Join(wantDirectory(identity.Directory, directory), wantArguments(identity.Arguments), wantEnvironment(identity.Environment))
		}},
	}
	for _, reader := range readers {
		t.Run(reader.name, func(t *testing.T) {
			directory := t.TempDir()
			command := exec.Command(os.Args[0], "-test.run=^TestParametersLongArgumentFixture$", argument)
			command.Dir = directory
			command.Env = append(os.Environ(), "CFO_PARAMETER_FIXTURE=1")
			command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED}
			output, err := command.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = command.Process.Kill(); _ = command.Wait() })
			pid := command.Process.Pid
			start, ok := StartTime(pid)
			if !ok {
				t.Fatal("read the child's start time")
			}
			if err := reader.read(pid, start, directory); err != nil {
				t.Fatalf("read the suspended child: %v", err)
			}
			running := make(chan struct{})
			go func() {
				_, _ = bufio.NewReader(output).ReadString('\n')
				close(running)
			}()
			resumeThreads(t, pid)
			deadline := time.After(30 * time.Second)
			for reads := 1; ; reads++ {
				if err := reader.read(pid, start, directory); err != nil {
					t.Fatalf("read %d, %s after the child was created: %v", reads, time.Since(start), err)
				}
				select {
				case <-running:
					return
				case <-deadline:
					t.Fatal("the child never reported that its own code runs")
				default:
				}
			}
		})
	}
}

// A process that has exited while a handle to it stays open keeps its pid
// and its PEB address, but its memory is gone, so it is reported unreadable
// and is never walked as if its parameter block were moving.
func TestReadersReportAProcessThatHasExitedUnreadable(t *testing.T) {
	command := exec.Command("cmd.exe", "/d", "/c", "exit 0")
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	pid := command.Process.Pid
	held, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		t.Fatal(err)
	}
	defer windows.CloseHandle(held)
	if err := command.Wait(); err != nil {
		t.Fatal(err)
	}
	handle, err := syscall.OpenProcess(processQueryInformation|processVMRead, false, uint32(pid))
	if err != nil {
		t.Fatalf("open the exited process: %v", err)
	}
	defer syscall.CloseHandle(handle)
	if _, err := parameterBlock(handle, pid); !errors.Is(err, windows.ERROR_PARTIAL_COPY) {
		t.Fatalf("reading the exited process failed with %v, want a partial copy, so this proves nothing", err)
	}
	start, ok := StartTime(pid)
	if !ok {
		t.Fatal("read the exited process's start time")
	}

	readers := []struct {
		name string
		read func() error
		want []error
	}{
		{"Parameters", func() error {
			directory, arguments, err := Parameters(pid)
			if directory != "" || arguments != nil {
				t.Errorf("Parameters read %q %q alongside its error", directory, arguments)
			}
			return err
		}, []error{ErrDirectoryUnreadable, ErrCommandLineUnreadable}},
		{"WorkingDirectory", func() error { _, err := WorkingDirectory(pid); return err }, []error{ErrDirectoryUnreadable}},
		{"Arguments", func() error { _, err := Arguments(pid); return err }, []error{ErrCommandLineUnreadable}},
		{"Environment", func() error { _, err := Environment(pid); return err }, nil},
		{"Identify", func() error { _, err := Identify(pid, start); return err }, nil},
	}
	for _, reader := range readers {
		err := reader.read()
		if err == nil {
			t.Errorf("%s of an exited process succeeded", reader.name)
		}
		for _, want := range reader.want {
			if !errors.Is(err, want) {
				t.Errorf("%s of an exited process = %v, want it to wrap %v", reader.name, err, want)
			}
		}
	}
}

// A walk is taken only once the PEB still points at the block it walked. One
// whose block moved is walked again, and a target that never holds still is
// reported, never trusted.
func TestWalkSteadyWalksAgainOnlyWhileTheBlockMoves(t *testing.T) {
	pid := Self()
	handle, err := syscall.OpenProcess(processQueryInformation|processVMRead, false, uint32(pid))
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.CloseHandle(handle)
	block, err := parameterBlock(handle, pid)
	if err != nil {
		t.Fatal(err)
	}
	moved := block + 8
	failed := errors.New("the walk failed")
	cases := []struct {
		name      string
		blocks    []uintptr
		walkErr   error
		wantWalks int
		wantErr   error
	}{
		{"a steady walk is taken", []uintptr{block}, nil, 1, nil},
		{"a steady failure is reported", []uintptr{block}, failed, 1, failed},
		{"a walk that found no block is reported", []uintptr{0}, failed, 1, failed},
		{"a walk across the move is walked again", []uintptr{moved, block}, nil, 2, nil},
		{"a failure across the move is walked again", []uintptr{moved, block}, failed, 2, failed},
		{"a block that never holds still is refused", []uintptr{moved}, nil, maxWalks, errKeptMoving},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			walks := 0
			err := walkSteady(handle, pid, func() (uintptr, error) {
				walked := test.blocks[min(walks, len(test.blocks)-1)]
				walks++
				return walked, test.walkErr
			})
			if walks != test.wantWalks {
				t.Errorf("walked %d times, want %d", walks, test.wantWalks)
			}
			if !errors.Is(err, test.wantErr) {
				t.Errorf("err = %v, want %v", err, test.wantErr)
			}
		})
	}
}

// resumeThreads lets every thread of the suspended process pid run.
func resumeThreads(t *testing.T, pid int) {
	t.Helper()
	if err := Resume(pid); err != nil {
		t.Fatal(err)
	}
}

// sameDir compares two Windows paths allowing for case and a trailing
// separator, which the parameter block and os.Getwd spell differently.
func sameDir(left, right string) bool {
	clean := func(value string) string {
		return strings.ToLower(strings.TrimRight(filepath.Clean(value), `\`))
	}
	return clean(left) == clean(right)
}
