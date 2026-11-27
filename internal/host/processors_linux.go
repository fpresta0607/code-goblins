package host

import (
	"fmt"
	"os"
	"time"

	"github.com/fpresta0607/code-goblins/internal/processor"
)

// terminalStart is the command spec's terminal starts, its own, and the mask
// of the processor threads it and everything it starts keep to: the fleet's
// (processor.MachineFleetThreads) for a terminal told to leave cores for
// apps, and 0, the threads the host may run on, for any other. Cores that
// cannot be read hold no terminal back: it runs on every one, as the start
// rule starts on processors it cannot read.
func terminalStart(spec Spec) ([]string, uintptr) {
	if !spec.ShouldLeaveCoresForApps {
		return spec.Args, 0
	}
	processors, err := processor.MachineFleetThreads()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s host: the terminal runs on every processor core, since this machine's cannot be read: %v\n", time.Now().UTC().Format(time.RFC3339Nano), err)
	}
	return spec.Args, processors
}
