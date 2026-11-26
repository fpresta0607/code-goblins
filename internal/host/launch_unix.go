//go:build linux || darwin

package host

import (
	"os"
	"os/exec"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// startDetached starts name with args in a session of its own, so no signal
// meant for this process's terminal or process group reaches it, and out of
// every tree this process runs in (execx.TreeVariable), so ending one of
// them, as a terminal's host ends its terminal's, ends no host it launched.
func startDetached(log *os.File, name string, args, env []string) (*exec.Cmd, error) {
	cmd := execx.Command(name, args...)
	cmd.Env = slices.DeleteFunc(slices.Clone(env), func(entry string) bool {
		return strings.HasPrefix(entry, execx.TreeVariable+"=")
	})
	cmd.Stdout, cmd.Stderr = log, log
	cmd.SysProcAttr.Setsid = true
	return cmd, cmd.Start()
}
