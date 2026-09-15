package fleet

import (
	"github.com/fpresta0607/code-goblins/internal/crewstate"
	"github.com/fpresta0607/code-goblins/internal/state"
	"time"
)

// Outcome is historical task evidence, independent of a running worker or
// current backend availability. A reported done outcome is not a merge proof.
type Outcome struct {
	Verb       string    `json:"verb"`
	Detail     string    `json:"detail"`
	RecordedAt time.Time `json:"recorded_at"`
}

func lastOutcome(dir, id string) *Outcome {
	lines, err := state.TailStatus(dir, id, 200)
	if err != nil {
		return nil
	}
	for i := len(lines) - 1; i >= 0; i-- {
		verb, detail, ok := crewstate.ParseStatusLine(lines[i])
		if !ok {
			continue
		}
		switch verb {
		case "done", "failed", "blocked", "needs-decision", "checks-passed", "paused":
			stamp, _ := state.SplitStatus(lines[i])
			return &Outcome{Verb: verb, Detail: detail, RecordedAt: stamp}
		}
	}
	return nil
}
