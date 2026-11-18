//go:build !windows

package supervisor

import "time"

var processStarted = time.Now()

// awakeTime is how long the machine has been awake since this process
// started: Go's monotonic clock, which on Linux and macOS stops while the
// machine sleeps.
func awakeTime() time.Duration {
	return time.Since(processStarted)
}
