package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

// consoleWindowRead bounds how long the console window of one process takes
// to read, so a console host that does not answer holds the start watch up
// no longer.
const consoleWindowRead = 10 * time.Second

// hideConsoleOnDesktop hides the console window that process pid's console shows on
// the desktop and says whether it hid one. Windows names a console's window
// only to a process attached to the console, so this program starts itself
// with no console of its own as proc.ConsoleWindowRole, which attaches to
// pid's console and prints the window, rather than attach the supervisor,
// whose own starts would then share a console that shows. The console of a
// process that runs above the supervisor, as a program the Overlord let run
// as an administrator does, is out of its reach: Windows lets it attach to
// none, and it hides nothing.
func hideConsoleOnDesktop(pid int) (bool, error) {
	isAbove, err := proc.RunsAbove(pid)
	if err != nil {
		return false, fmt.Errorf("start watch: read the console window of pid %d: %w", pid, err)
	}
	if isAbove {
		return false, nil
	}
	self, err := os.Executable()
	if err != nil {
		return false, fmt.Errorf("start watch: find this program to read a console's window: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), consoleWindowRead)
	defer cancel()
	command := execx.CommandContext(ctx, self, strconv.Itoa(pid))
	command.Args[0] = proc.ConsoleWindowRole
	command.SysProcAttr.CreationFlags |= windows.DETACHED_PROCESS
	output, err := command.Output()
	if exited := (*exec.ExitError)(nil); errors.As(err, &exited) {
		return false, fmt.Errorf("start watch: read the console window of pid %d: %w: %s", pid, err, strings.TrimSpace(string(exited.Stderr)))
	}
	if err != nil {
		return false, fmt.Errorf("start watch: read the console window of pid %d: %w", pid, err)
	}
	window, err := strconv.ParseUint(strings.TrimSpace(string(output)), 10, 64)
	if err != nil {
		return false, fmt.Errorf("start watch: read the console window of pid %d: %w", pid, err)
	}
	return proc.HideConsoleWindow(windows.HWND(window))
}
