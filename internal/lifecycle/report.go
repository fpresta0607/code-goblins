package lifecycle

import (
	"strings"

	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

func Report(directory string, record state.Lifecycle) error {
	detail := record.Phase + ": " + strings.Join(append(append(append([]string{record.Reason}, record.Stopped...), record.Kept...), record.Problems...), "; ")
	if status := record.TeardownStatus(); status != "" {
		detail += "; " + status
	}
	if _, err := wake.AppendOnce(directory, "lifecycle/"+record.ID+"/"+record.Operation, "notify", record.ID, detail); err != nil {
		return err
	}
	_, err := wake.PublishEpisode(directory)
	return err
}
