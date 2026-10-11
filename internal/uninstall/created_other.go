//go:build !windows

package uninstall

import (
	"os"
	"time"
)

// createdAt is when the file at path was last written, the nearest a file
// system without creation times says.
func createdAt(path string) (time.Time, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}, false
	}
	return info.ModTime(), true
}
