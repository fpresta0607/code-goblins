//go:build !windows

package host

import (
	"errors"
	"fmt"
	"time"
)

// errLaunchNotPorted is what launching or closing a host returns on this
// system: a host started off Windows does not yet leave its launcher, and
// nothing reads whether it still runs.
var errLaunchNotPorted = fmt.Errorf("host: launching and closing a native terminal's host is not ported to this system yet: %w", errors.ErrUnsupported)

// Launch refuses, since no host is launched on this system yet.
func Launch(stateDir string, command, env []string, spec Spec) (Record, error) {
	return Record{}, errLaunchNotPorted
}

// Close refuses, since no host is launched on this system yet.
func Close(stateDir string, launched Record, answerWait time.Duration) error {
	return errLaunchNotPorted
}

// Running reports no host running, since no host is launched on this system
// yet.
func Running(record Record) bool { return false }
