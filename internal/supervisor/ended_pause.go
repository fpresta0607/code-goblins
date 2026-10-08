package supervisor

import (
	"errors"
	"os"

	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// pauseTookHold says a pause that failed while its native goblin ran took
// effect since: the goblin's terminal has ended, its host's record gone or
// naming a host that no longer runs. Such a goblin is paused, and resumes as
// a paused one does.
func pauseTookHold(stateDir, backend string, record state.Lifecycle) bool {
	if backend != "native" || record.Action != "pause" || record.Phase != "failed" {
		return false
	}
	terminal, err := host.ReadRecord(stateDir, record.ID)
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	return err == nil && !host.Running(terminal)
}
