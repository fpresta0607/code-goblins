//go:build !windows && !linux && !darwin

package host

import (
	"errors"
	"fmt"
)

// errNotPorted is what running or reaching a host returns on this system:
// a native terminal runs in a Windows pseudo console or a Linux or macOS
// pseudo terminal, and neither is here.
var errNotPorted = fmt.Errorf("host: native terminals are not ported to this system yet: %w", errors.ErrUnsupported)

// Run refuses, since no host runs on this system.
func Run(stateDir string, spec Spec) error { return errNotPorted }

// dialPipe refuses, since no host runs on this system.
func dialPipe(name string, hostPID int) (hostConnection, error) { return nil, errNotPorted }
