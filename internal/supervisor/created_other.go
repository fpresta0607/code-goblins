//go:build !windows

package supervisor

import "time"

// fileCreated is always zero, since other systems keep no creation time Go
// can read and a modification time moves with every write.
func fileCreated(string) time.Time {
	return time.Time{}
}
