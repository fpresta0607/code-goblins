//go:build linux || darwin

package home

import (
	"errors"
	"os"
	"path/filepath"
)

// defaultRoot is $XDG_DATA_HOME/code-goblins, or ~/.local/share/code-goblins
// when XDG_DATA_HOME is unset, empty or relative, which the XDG base
// directory rules say to ignore. macOS takes the same folder, not
// ~/Library/Application Support, whose space every hook and card command
// would have to quote (docs/plans/2026-11-11-linux-port.md).
func defaultRoot() (string, error) {
	if data := os.Getenv("XDG_DATA_HOME"); filepath.IsAbs(data) {
		return filepath.Join(filepath.Clean(data), "code-goblins"), nil
	}
	user := os.Getenv("HOME")
	if !filepath.IsAbs(user) {
		return "", errors.New("home: neither XDG_DATA_HOME nor HOME names an absolute folder, so there is no per-user folder for a CEO home; set GOBLINS_HOME")
	}
	return filepath.Join(filepath.Clean(user), ".local", "share", "code-goblins"), nil
}
