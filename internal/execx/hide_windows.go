package execx

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// hide keeps cmd from opening a console window. A console program shares
// the console of the process that starts it, so a start from a process that
// has one, shown or hidden, opens no window, and the Overlord's Ctrl-C and a
// gate's cancellation still reach the child. A process with none, such as a
// scheduled task's, a detached process's or a windowed program's child,
// would give the child a new console, which Windows shows as a window, so
// that start asks for a console with no window. A caller adds its own
// creation flags with |=: CREATE_NEW_CONSOLE and DETACHED_PROCESS each
// override CREATE_NO_WINDOW.
func hide(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{}
	if _, err := windows.GetConsoleCP(); err != nil {
		cmd.SysProcAttr.CreationFlags = windows.CREATE_NO_WINDOW
	}
}
