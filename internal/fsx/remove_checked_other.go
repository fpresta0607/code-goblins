//go:build !windows

package fsx

import (
	"context"
	"errors"
	"fmt"
)

// RemoveFileWhen refuses: deleting a file only after checking the same open
// file is not ported to this system yet.
func RemoveFileWhen(ctx context.Context, path string, check func([]byte) (bool, error)) (bool, error) {
	return false, fmt.Errorf("fsx: removing %s after a check is not ported to this system yet: %w", path, errors.ErrUnsupported)
}
