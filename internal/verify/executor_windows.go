package verify

import (
	"os/exec"

	"golang.org/x/sys/windows"
)

// inGroupOfItsOwn starts command in a console group of its own, which a
// Ctrl+Break reaches without reaching this process.
func inGroupOfItsOwn(command *exec.Cmd) {
	command.SysProcAttr.CreationFlags |= windows.CREATE_NEW_PROCESS_GROUP
}

// interruptGroup sends a Ctrl+Break to the console group process pid leads.
func interruptGroup(pid int) error {
	return windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(pid))
}
