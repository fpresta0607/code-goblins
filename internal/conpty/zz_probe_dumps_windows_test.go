package conpty

import (
	"os"

	"golang.org/x/sys/windows"
)

// Probe, not committed: Go starts with SEM_NOGPFAULTERRORBOX, which the
// console host inherits and which keeps Windows Error Reporting from dumping
// it when it crashes, so the probe gives it the default error mode.
func init() {
	if os.Getenv("CONPTY_PROBE_DUMPS") != "" {
		windows.SetErrorMode(0)
	}
}
