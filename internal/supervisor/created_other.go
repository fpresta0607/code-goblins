//go:build !windows

package supervisor

import (
	"os"
	"time"
)

// fileCreated is when a file was last written, since other systems keep no
// creation time Go can read; zero when it cannot be read.
func fileCreated(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime().UTC()
}
