//go:build !windows

package update

import (
	"errors"
	"fmt"
	"os"
)

// Hold refuses: holding a file against every change while it is hashed is not
// ported to this system yet, whose updater comes later.
func Hold(path string) (*os.File, string, error) {
	return nil, "", fmt.Errorf("update: holding %s is not ported to this system yet: %w", path, errors.ErrUnsupported)
}
