//go:build !windows

package update

import (
	"errors"
	"fmt"
)

// writeSynced refuses: an update's durable write is not ported to this
// system yet, whose updater comes later.
func writeSynced(path string, data []byte) error {
	return fmt.Errorf("update: writing %s durably is not ported to this system yet: %w", path, errors.ErrUnsupported)
}
