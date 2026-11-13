//go:build !windows

package fleettree

import (
	"errors"
	"fmt"
)

// Processes refuses: listing the machine's processes is not ported to this
// system yet.
func Processes() ([]Process, error) {
	return nil, fmt.Errorf("fleettree: listing processes is not ported to this system yet: %w", errors.ErrUnsupported)
}
