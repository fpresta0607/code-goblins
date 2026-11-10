//go:build !windows

package fsx

import "path/filepath"

// resolve is filepath.EvalSymlinks: elsewhere it follows every link.
func resolve(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}
