//go:build !windows

package spawn

import (
	"errors"
	"fmt"
)

// cmdProgram refuses a script: a native terminal runs only on Windows so far,
// where cmd /c starts it.
func cmdProgram(path string, args ...string) ([]string, error) {
	return nil, fmt.Errorf("spawn: %s is a script, and running one in a native terminal is not ported to this system yet: %w", path, errors.ErrUnsupported)
}

// UserEnvironment refuses: the environment a new process of this user gets
// is read from the Windows user token, which this system lacks.
func UserEnvironment() ([]string, error) {
	return nil, fmt.Errorf("spawn: reading the user's own environment is not ported to this system yet: %w", errors.ErrUnsupported)
}
