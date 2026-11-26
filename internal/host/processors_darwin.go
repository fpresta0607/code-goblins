package host

import (
	"fmt"
	"os"
	"time"
)

// terminalProcessors is 0, for the threads the host may run on: macOS keeps
// no process to processor threads, and what keeps a terminal told to leave
// cores for apps off them there is not ported yet.
func terminalProcessors(spec Spec) uintptr {
	if spec.ShouldLeaveCoresForApps {
		fmt.Fprintf(os.Stderr, "%s host: the terminal runs on every processor core, since keeping it off the cores left to apps is not ported to this system yet\n", time.Now().UTC().Format(time.RFC3339Nano))
	}
	return 0
}
