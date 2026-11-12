//go:build !windows

package supervisor

import (
	"context"
	"errors"
	"fmt"
)

// OSRunLauncher would start a run item in a native terminal, which this
// system has none of yet.
type OSRunLauncher struct {
	HostCommand []string
}

// Launch refuses, since native terminals are not ported to this system yet.
func (o OSRunLauncher) Launch(_ context.Context, l RunLaunch) (RunStarted, error) {
	return RunStarted{}, fmt.Errorf("supervisor: starting a run item is not ported to this system yet: %w", errors.ErrUnsupported)
}
