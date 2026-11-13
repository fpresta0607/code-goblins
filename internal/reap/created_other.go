//go:build !windows

package reap

import "time"

// createdAt is when a directory was made, zero when that cannot be read:
// reading a creation time is not ported to this system yet.
func createdAt(path string) time.Time {
	return time.Time{}
}
