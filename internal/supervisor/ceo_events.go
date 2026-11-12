package supervisor

import (
	"errors"
	"slices"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/nativehook"
)

// followNativeCEO does for a registered Codex or pi CEO, from the native
// events the supervisor just ingested, what Claude Code's own hooks do for a
// Claude CEO: when its turn ends idle it raises the idle wake, and around a
// compaction it writes the checkpoint and queues the wake that names it. Its
// native hook only spools each event, so the harness waits on it no longer
// than Claude Code waits on its own. Only the registered CEO's own session
// counts, and a compaction only while that CEO holds the primary home, so a
// goblin's or another session's events change nothing.
func (s *Service) followNativeCEO(events []nativehook.Event) error {
	isCEOs := func(e nativehook.Event) bool { return e.Role == "ceo" && e.TaskID == "" }
	if !slices.ContainsFunc(events, isCEOs) {
		return nil
	}
	h := s.Store.Home
	primary, live := livePrimary(h.State)
	if !live || primary.Agent != "codex" && primary.Agent != "pi" {
		return nil
	}
	session := primary.Agent + "/" + ceoSession(h.State, primary)
	isSettled := false
	var err error
	for _, e := range events {
		if !isCEOs(e) || e.Harness+"/"+e.SessionID != session {
			continue
		}
		switch e.Kind {
		case "compacting", "compacted":
			holder, lockErr := lock.Read(h.State)
			if s.Options.CompactCEO != nil && lockErr == nil && holder.VerifiedAlive() && holder.PID == primary.Process.PID && home.IsPrimary(h) {
				err = errors.Join(err, s.Options.CompactCEO(h, e, time.Now()))
			}
		default:
			// A turn ended idle only when nothing after it began another.
			isSettled = e.Kind == "settled"
		}
	}
	dispatch := s.Options.Dispatch
	if !isSettled || dispatch == nil || dispatch.Memory == nil {
		return err
	}
	memory, readErr := dispatch.Memory()
	if readErr != nil {
		return errors.Join(err, readErr)
	}
	_, wakeErr := IdleTurnWake(h, memory, time.Now().UTC())
	return errors.Join(err, wakeErr)
}
