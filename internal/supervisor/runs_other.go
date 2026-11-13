//go:build !windows

package supervisor

import (
	"context"
	"errors"
	"fmt"
)

// Launch refuses, since native terminals are not ported to this system yet.
func (o OSRunLauncher) Launch(_ context.Context, l RunLaunch) (RunStarted, error) {
	return RunStarted{}, fmt.Errorf("supervisor: starting a run item is not ported to this system yet: %w", errors.ErrUnsupported)
}
