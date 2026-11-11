package supervisor

import (
	"fmt"
	"time"
)

// handleLine is how many handles one process may hold before the CFO is woken
// for it. On a working machine the busiest process, Microsoft Defender, holds
// about 9,000 and the whole machine about 190,000, while the system handle
// table a pause reads is bounded near 6.7 million: a recursive search of the
// whole drive that outlived its shell passed this line within minutes and
// filled the table on 2026-11-11.
const handleLine = 1_000_000

// HandleHolder is the process holding the most handles open on the machine,
// as one system process list counts them: its pid, program and start, how
// many it holds, and its command line as a wake may show it, read only for
// one past handleLine.
type HandleHolder struct {
	PID     int       `json:"pid"`
	Name    string    `json:"name"`
	Started time.Time `json:"started"`
	Handles uint32    `json:"handles"`
	Command string    `json:"command,omitempty"`
}

// checkHandles wakes the CFO once for each process past handleLine, naming it
// by its pid and command, so a leak is ended before it fills the handle table
// and a pause can no longer read a goblin's job.
func (s *Service) checkHandles(w *fleetWakes, now time.Time) error {
	dispatch := s.Options.Dispatch
	if dispatch == nil || dispatch.TopHandleHolder == nil {
		return nil
	}
	holder, err := dispatch.TopHandleHolder()
	if err != nil {
		return nil
	}
	if holder.Handles < handleLine {
		w.HandleHolder = ""
		return nil
	}
	identity := fmt.Sprintf("%d@%s", holder.PID, holder.Started.Format(time.RFC3339Nano))
	if identity == w.HandleHolder {
		return nil
	}
	if err := raiseFleetWake(s.Store.Home.State, "memory", "handles", handleHolderDetail(holder, now)); err != nil {
		return err
	}
	w.HandleHolder = identity
	return nil
}

// handleHolderDetail is the handle wake's text: the process, what it holds
// and how long it has run, and how to end it.
func handleHolderDetail(holder HandleHolder, now time.Time) string {
	command := holder.Command
	if command == "" {
		command = "its command could not be read"
	}
	return fmt.Sprintf("handles: %s pid %d, started %s and running %s, holds %d handles, past the %d line: %s. The system handle table fills past about 6.7 million, and then no pause can read a goblin's job. Next: if it is the fleet's, cfo reap lists it and cfo reap --force %d --apply ends it by its pid; otherwise tell the Overlord.",
		holder.Name, holder.PID, holder.Started.Format(time.RFC3339), now.Sub(holder.Started).Round(time.Second), holder.Handles, handleLine, command, holder.PID)
}
