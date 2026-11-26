package host

import (
	"fmt"
	"time"

	"golang.org/x/sys/unix"
)

// machineStarted is when the machine started, from how long it has run. The
// kernel counts that in whole seconds, so it is taken a second earlier, which
// never drops a proof given just after the start.
func machineStarted() (time.Time, error) {
	var info unix.Sysinfo_t
	if err := unix.Sysinfo(&info); err != nil {
		return time.Time{}, fmt.Errorf("host: read how long the machine has run: %w", err)
	}
	return time.Now().Add(-time.Duration(info.Uptime+1) * time.Second), nil
}
