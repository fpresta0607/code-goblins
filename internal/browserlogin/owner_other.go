//go:build !windows

package browserlogin

import (
	"errors"
	"fmt"
)

// ownerOnly refuses: keeping a folder to its owner alone is not ported to
// this system yet.
func ownerOnly(path string) error {
	return fmt.Errorf("browserlogin: keeping %s to its owner is not ported to this system yet: %w", path, errors.ErrUnsupported)
}
