package conpty

import (
	"errors"
	"os"
)

// Probe, not committed: CONPTY_PROBE_INBOX runs this process's consoles on
// the inbox conhost, as a console host that cannot be placed does.
func init() {
	if os.Getenv("CONPTY_PROBE_INBOX") != "" {
		consoleHostFolder = func() (string, error) { return "", errors.New("the probe measures the inbox conhost") }
	}
}
