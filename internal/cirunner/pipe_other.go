//go:build !windows

package cirunner

import (
	"context"
	"errors"
	"fmt"
)

// Ask refuses: the desktop CI runner's service runs only on Windows so far,
// so no pipe answers here.
func Ask(ctx context.Context, name string, request Request, reply func(Reply)) error {
	return fmt.Errorf("cirunner: the desktop CI runner is not ported to this system yet: %w", errors.ErrUnsupported)
}
