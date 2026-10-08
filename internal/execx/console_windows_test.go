package execx

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

var (
	getConsoleWindow      = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleWindow")
	getConsoleProcessList = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleProcessList")
)

// TestConsoleFixture is not a test: it is the parent and the child processes
// TestProcessesStartedThroughExecxOpenNoConsoleWindow starts.
func TestConsoleFixture(t *testing.T) {
	report := os.Getenv("EXECX_CONSOLE_REPORT")
	switch os.Getenv("EXECX_CONSOLE_FIXTURE") {
	case "parent":
		os.Exit(startConsoleChild(os.Getenv("EXECX_CONSOLE_ENTRY"), report))
	case "child":
		// A console window of its own is what Windows shows on the desktop;
		// a console with none, or no console, reads as window 0.
		window, _, _ := getConsoleWindow.Call()
		processes := make([]uint32, 64)
		count, _, _ := getConsoleProcessList.Call(uintptr(unsafe.Pointer(&processes[0])), uintptr(len(processes)))
		shares := slices.Contains(processes[:min(int(count), len(processes))], uint32(os.Getppid()))
		if err := os.WriteFile(report+".tmp", fmt.Appendf(nil, "window=%d shares=%t", window, shares), 0o600); err != nil {
			os.Exit(1)
		}
		if err := os.Rename(report+".tmp", report); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
}

// startConsoleChild starts the child fixture through one of execx's entry
// points and waits for its report.
func startConsoleChild(entry, report string) int {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	request := Request{Name: os.Args[0], Args: []string{"-test.run=^TestConsoleFixture$"}, Env: append(os.Environ(), "EXECX_CONSOLE_FIXTURE=child")}
	var err error
	switch entry {
	case "run":
		_, err = OSRunner{}.Run(ctx, request)
	case "start":
		err = OSRunner{}.Start(ctx, request)
	case "command":
		command := CommandContext(ctx, request.Name, request.Args...)
		command.Env = request.Env
		err = command.Run()
	default:
		err = fmt.Errorf("unknown entry %q", entry)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	for ctx.Err() == nil {
		if _, err := os.Stat(report); err == nil {
			return 0
		}
		time.Sleep(20 * time.Millisecond)
	}
	fmt.Fprintln(os.Stderr, "the child never reported")
	return 1
}

// A console program started by a process with no console of its own, as a
// scheduled task's, a detached process's or a windowed program's children
// are, gets a new console, and Windows shows that console as a window unless
// the start asks for none. A process that has a console passes it on, so its
// children share it, and the Overlord's Ctrl-C and a gate's cancellation
// reach them as they reach it.
func TestProcessesStartedThroughExecxOpenNoConsoleWindow(t *testing.T) {
	for _, parent := range []struct {
		name   string
		flags  uint32
		shares bool
	}{
		{name: "parent without a console", flags: windows.DETACHED_PROCESS},
		{name: "parent with a hidden console", flags: windows.CREATE_NO_WINDOW, shares: true},
	} {
		for _, entry := range []string{"run", "start", "command"} {
			t.Run(parent.name+"/"+entry, func(t *testing.T) {
				report := filepath.Join(t.TempDir(), "report")
				command := exec.Command(os.Args[0], "-test.run=^TestConsoleFixture$")
				command.Env = append(os.Environ(), "EXECX_CONSOLE_FIXTURE=parent", "EXECX_CONSOLE_ENTRY="+entry, "EXECX_CONSOLE_REPORT="+report)
				command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: parent.flags}
				if output, err := command.CombinedOutput(); err != nil {
					t.Fatalf("the parent failed: %v: %s", err, output)
				}
				// The parent fixture returns once the report's new name
				// exists, which can be while the child's rename still holds
				// it, so it is read as fleet programs read.
				got, err := fsx.ReadFile(report)
				if err != nil {
					t.Fatal(err)
				}
				if want := fmt.Sprintf("window=0 shares=%t", parent.shares); string(got) != want {
					t.Fatalf("the child reported %q, want %q (a nonzero window is a console window on the Overlord's desktop)", got, want)
				}
			})
		}
	}
}
