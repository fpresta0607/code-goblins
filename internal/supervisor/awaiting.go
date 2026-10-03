package supervisor

import (
	"time"

	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/nativehook"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// Awaiting is a delivery typed into a native terminal and submitted that its
// harness has not yet reported taking: the CFO's exact native recipient, or a
// goblin's by its task and spawn generation. A harness inside a turn, or one
// slow to start its next, takes the text later, and its prompt hook says so
// only then, so until then the delivery is sent, not failed.
type Awaiting struct {
	Host       string                  `json:"host,omitempty"`
	Task       string                  `json:"task,omitempty"`
	Generation string                  `json:"generation,omitempty"`
	Harness    string                  `json:"harness,omitempty"`
	Since      time.Time               `json:"since"`
	Recipient  nativehook.CFORecipient `json:"recipient,omitzero"`
	// QuietSince is the last time the terminal's screen showed a turn under
	// way, or Since when it has shown none.
	QuietSince time.Time `json:"quiet_since"`
}

// sentToCFO and sentToGoblin are what a delivery that awaits its hook says.
const (
	sentToCFO    = "Sent. The CFO reads it when its current turn ends."
	sentToGoblin = "Sent. The goblin reads it when its current turn ends."
)

const (
	// deliveryQuiet is how long a terminal may show no turn, with no report
	// that its harness took a delivery, before the delivery counts as not
	// arrived. A harness has taken tens of seconds to start the turn that
	// reads it, which the five second confirmation called a failure.
	deliveryQuiet = 3 * time.Minute
	// deliveryForget is how long a delivery that never arrived is still
	// watched for a late report.
	deliveryForget = 24 * time.Hour
	// quietStep is how far the last look that showed a turn must move before
	// it is saved again, so a long turn does not write the state every cycle.
	quietStep = 30 * time.Second
)

// terminalLook is what the supervisor sees of the terminal a delivery waits
// in: gone, or its screen showing a turn under way.
type terminalLook struct {
	Gone    bool
	Working bool
}

// recipient is who a delivery waits on, as the Overlord reads it.
func (a Awaiting) recipient() string {
	if a.Task != "" {
		return "the goblin"
	}
	return "the CFO"
}

// tookPrompt reports whether the harness a delivery waits on reported taking
// a prompt at or after it was submitted. The caller holds the store lock.
func (s *Store) tookPrompt(a Awaiting) bool {
	for _, session := range s.db.Sessions {
		ours := a.Host != "" && a.Recipient.HostID == a.Host && a.Recipient.Harness == a.Harness &&
			session.Role == "cfo" && session.Harness == a.Harness && session.NativeID == a.Recipient.SessionID &&
			a.Recipient.Matches(session.PromptRecipient)
		if a.Task != "" {
			ours = session.TaskID == a.Task && session.Generation == a.Generation
		}
		if ours && !session.PromptAt.IsZero() && !session.PromptAt.Before(a.Since) {
			return true
		}
	}
	return false
}

// settleDeliveries settles each delivery that awaits its hook: delivered once
// the harness reported taking a prompt since the submit, and not arrived, in
// words that say what to do, only when its terminal is gone or has shown no
// turn for deliveryQuiet with still no report. A delivery that did not arrive
// is still watched, so a late report delivers it after all. look reads each
// terminal away from the store's lock.
func (s *Store) settleDeliveries(now time.Time, look func(Awaiting) terminalLook) error {
	s.mu.Lock()
	looks := map[string]terminalLook{}
	var sent []Action
	for _, a := range s.db.Actions {
		if a.Awaiting != nil && a.Status == "running" {
			sent = append(sent, a)
		}
	}
	s.mu.Unlock()
	for _, a := range sent {
		looks[a.ID] = look(*a.Awaiting)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for i := range s.db.Actions {
		a := &s.db.Actions[i]
		if a.Awaiting == nil {
			continue
		}
		waiting, who := *a.Awaiting, a.Awaiting.recipient()
		seen, looked := looks[a.ID]
		switch {
		case s.tookPrompt(waiting):
			a.Status, a.Awaiting, a.Advice = "succeeded", nil, ""
			a.Message = "Taken by " + who + " in its terminal, as its hook reported."
			s.deliveredReview(a.ID, waiting.Task)
		case a.Status != "running":
			// It did not arrive; only a late report changes that.
			if now.Sub(waiting.Since) < deliveryForget {
				continue
			}
			a.Awaiting = nil
		case !looked:
			continue
		case seen.Gone:
			a.Status = "uncertain"
			if waiting.Task != "" || !waiting.Recipient.Valid() {
				a.Awaiting = nil
			}
			a.Advice = "The terminal of " + who + " closed before it took your answer. Give " + who + " your answer once it is running again."
			a.Message = a.Advice
		case seen.Working:
			if now.Sub(waiting.QuietSince) < quietStep {
				continue
			}
			waiting.QuietSince = now
			a.Awaiting = &waiting
		case now.Sub(waiting.QuietSince) >= deliveryQuiet:
			a.Status = "uncertain"
			a.Advice = "Your answer was typed for " + who + ", which has not picked it up. Open its terminal and press Enter if your answer is waiting in its box; if it is not there, type it to " + who + "."
			a.Message = a.Advice
		default:
			continue
		}
		a.UpdatedAt, changed = now, true
	}
	if !changed {
		return nil
	}
	s.updateQuestionOutcomes()
	return s.save()
}

// deliveredReview marks the review item whose answer action id carried as
// delivered, when task, whose terminal took it, is the item's reporter: an
// answer handed to the CFO for a goblin that restarted or ended never reached
// the goblin. The caller holds the store lock.
func (s *Store) deliveredReview(action, task string) {
	for i := range s.db.Reviews {
		if s.db.Reviews[i].AnswerID == action && s.db.Reviews[i].Task == task {
			s.db.Reviews[i].Delivered = true
		}
	}
}

// behindGoblinsTurn is the outcome of a delivery typed for goblin task while
// it was in a turn. A native goblin's harness reports taking it, so the
// delivery is sent and awaits that report; a goblin in a Herdr pane reports
// nothing the board can wait on, so its delivery reads as before.
func (s *Service) behindGoblinsTurn(task string, since time.Time, unwatched string) Evaluation {
	meta, err := state.ReadTaskMeta(s.Store.Home.State, task)
	if err != nil || meta.Backend != "native" {
		return Evaluation{Reason: unwatched}
	}
	return Evaluation{Reason: sentToGoblin, Awaiting: &Awaiting{Task: meta.ID, Generation: meta.SpawnGen, Harness: meta.Harness, Since: since}}
}

// lookAtTerminal reads the terminal a delivery waits in: gone when its host
// no longer answers for it or its goblin restarted or ended, working when its
// screen shows a turn under way.
func (s *Service) lookAtTerminal(a Awaiting) terminalLook {
	id := a.Host
	if a.Task != "" {
		meta, err := state.ReadTaskMeta(s.Store.Home.State, a.Task)
		if err != nil || meta.SpawnGen != a.Generation {
			return terminalLook{Gone: true}
		}
		id = meta.ID
	} else if a.Recipient.Valid() {
		recipient, err := NativeCFORecipient(s.Store.Home.State)
		if err != nil || !a.Recipient.Matches(recipient) {
			return terminalLook{Gone: true}
		}
	}
	record, err := host.ReadRecord(s.Store.Home.State, id)
	if err != nil || !host.Running(record) {
		return terminalLook{Gone: true}
	}
	if a.Task == "" && a.Recipient.Valid() && (record.HostPID != a.Recipient.HostPID || record.ChildPID != a.Recipient.ProgramPID || !record.ChildStart.Equal(a.Recipient.ProgramStart)) {
		return terminalLook{Gone: true}
	}
	screens, readable := harness.NativeScreens(harness.Kind(a.Harness))
	if !readable {
		return terminalLook{}
	}
	rows, err := host.ReadScreen(record)
	return terminalLook{Working: err == nil && screens.IsWorking(rows)}
}
