//go:build linux || darwin

package host

import (
	"time"

	"github.com/fpresta0607/code-goblins/internal/pty"
)

// emulatedTerminal is a pseudo terminal whose screen is the one the host's
// emulator keeps from the terminal's output, since no system holds a pseudo
// terminal's screen to read back, as Windows does a pseudo console's.
type emulatedTerminal struct {
	*pty.Terminal
	output *history
}

// Screen is the screen the host's emulator holds now. It never waits, so it
// takes no timeout.
func (t emulatedTerminal) Screen(time.Duration) ([]string, error) {
	t.output.mu.Lock()
	defer t.output.mu.Unlock()
	return t.output.screen.Rows(), nil
}
