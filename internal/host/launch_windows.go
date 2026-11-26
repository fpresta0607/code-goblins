package host

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/priority"
)

// startHost starts the host with a hidden console of its own, so nothing the
// host runs opens a window, in its own process group, one class above the
// work from its start, which the launch waits on, and out of this process's
// job. Where the job forbids breaking away, the host starts inside it
// instead and is contained.
func startHost(log *os.File, name string, args, env []string) (*exec.Cmd, bool, error) {
	start := func(flags uint32) (*exec.Cmd, error) {
		cmd := execx.Command(name, args...)
		cmd.Env = env
		cmd.Stdout, cmd.Stderr = log, log
		cmd.SysProcAttr.CreationFlags |= flags
		return cmd, cmd.Start()
	}
	flags := uint32(windows.CREATE_NO_WINDOW|windows.CREATE_NEW_PROCESS_GROUP) | priority.StartAboveTheWork()
	cmd, err := start(flags | windows.CREATE_BREAKAWAY_FROM_JOB)
	if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return cmd, false, err
	}
	fmt.Fprintf(log, "%s host: cannot break away from the job of the process that launched it (pid %d), so this host runs inside that job and ends when it closes\n", time.Now().UTC().Format(time.RFC3339Nano), os.Getpid())
	cmd, err = start(flags)
	return cmd, true, err
}
