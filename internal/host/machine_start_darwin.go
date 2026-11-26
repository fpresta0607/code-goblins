package host

import (
	"fmt"
	"time"

	"golang.org/x/sys/unix"
)

// machineStarted is when the machine started (kern.boottime).
func machineStarted() (time.Time, error) {
	boot, err := unix.SysctlTimeval("kern.boottime")
	if err != nil {
		return time.Time{}, fmt.Errorf("host: read when the machine started: %w", err)
	}
	return time.Unix(boot.Unix()), nil
}
