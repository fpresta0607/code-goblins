package host

import (
	"os"
	"os/exec"
)

// startHost starts the host detached from this process (startDetached). With
// no systemd, a session of its own is all that keeps it running after its
// launcher: the supervisor runs as a launchd agent that abandons its process
// group, so an update of the supervisor ends no host.
func startHost(log *os.File, name string, args, env []string) (*exec.Cmd, bool, error) {
	cmd, err := startDetached(log, name, args, env)
	return cmd, false, err
}
