//go:build !windows

package supervisor

import (
	"io/fs"
	"time"
)

// created is always zero, since other systems keep no creation time Go can
// read and a modification time moves with every write.
func created(fs.FileInfo) time.Time {
	return time.Time{}
}
