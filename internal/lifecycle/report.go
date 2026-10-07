package lifecycle

import (
	"strings"

	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// Report tells the CFO how a pause, resume or stop ended. Its words start
// with the lifecycle's own verb, as the task's status log does, never a
// goblin's notify verb: a pause or stop that did not finish is the CFO's to
// see and act on, not the goblin failing or asking anything, so the board
// never reads it as either.
func Report(directory string, record state.Lifecycle) error {
	detail := "lifecycle-" + record.Phase + ": " + strings.Join(append(append(append([]string{record.Reason}, record.Stopped...), record.Kept...), record.Problems...), "; ")
	if status := record.TeardownStatus(); status != "" {
		detail += "; " + status
	}
	if _, err := wake.AppendOnce(directory, "lifecycle/"+record.ID+"/"+record.Operation, "notify", record.ID, detail); err != nil {
		return err
	}
	_, err := wake.PublishEpisode(directory)
	return err
}
