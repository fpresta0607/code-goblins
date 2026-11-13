//go:build !windows

package watch

import (
	"errors"
	"fmt"
)

// newCIRunnerWatch refuses: the desktop CI runner is not ported to this
// system yet, so there is none to watch.
func newCIRunnerWatch(stateDir, pipe string) (*ciRunnerWatch, error) {
	return nil, fmt.Errorf("watch: the desktop CI runner is not ported to this system yet: %w", errors.ErrUnsupported)
}
