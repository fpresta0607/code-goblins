//go:build !windows

package fleettree

import (
	"errors"
	"fmt"
)

// Listeners refuses: listing the processes that listen on a port is not
// ported to this system yet.
func Listeners() (map[int][]int, error) {
	return nil, fmt.Errorf("fleettree: listing listening ports is not ported to this system yet: %w", errors.ErrUnsupported)
}
